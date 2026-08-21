package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func decodeForAssertions(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root map[string]interface{}
	if err := dec.Decode(&root); err != nil {
		t.Fatalf("failed to decode result body: %v\nbody: %s", err, body)
	}
	return root
}

func messagesOf(t *testing.T, root map[string]interface{}) []interface{} {
	t.Helper()
	messages, ok := root["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages field missing or not an array: %#v", root)
	}
	return messages
}

func contentOf(t *testing.T, msg interface{}) []interface{} {
	t.Helper()
	m, ok := msg.(map[string]interface{})
	if !ok {
		t.Fatalf("message is not an object: %#v", msg)
	}
	content, ok := m["content"].([]interface{})
	if !ok {
		t.Fatalf("message content is not an array: %#v", m["content"])
	}
	return content
}

func partField(t *testing.T, part interface{}, field string) string {
	t.Helper()
	m, ok := part.(map[string]interface{})
	if !ok {
		t.Fatalf("content part is not an object: %#v", part)
	}
	s, _ := m[field].(string)
	return s
}

func TestFixToolResultPairing_NoChangeWhenAlreadyPaired(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"lookup","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"ok"}]}
	]}`)

	fixed, changed := fixToolResultPairing(body)

	if changed {
		t.Fatalf("expected no change for an already-paired conversation, got changed=true")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected pass-through bytes when unchanged, got a different slice")
	}
}

func TestFixToolResultPairing_EmptyBodyIsUntouched(t *testing.T) {
	fixed, changed := fixToolResultPairing(nil)
	if changed || fixed != nil {
		t.Fatalf("expected empty body to pass through untouched, got changed=%v fixed=%q", changed, fixed)
	}
}

func TestFixToolResultPairing_MalformedJSONReturnsUnchanged(t *testing.T) {
	body := []byte(`{"messages": [ not valid json`)
	fixed, changed := fixToolResultPairing(body)
	if changed {
		t.Fatalf("expected malformed JSON to be left unchanged")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected malformed JSON bytes to pass through unmodified")
	}
}

func TestFixToolResultPairing_NonArrayMessagesFieldIsNoOp(t *testing.T) {
	body := []byte(`{"messages": "not an array"}`)
	fixed, changed := fixToolResultPairing(body)
	if changed {
		t.Fatalf("expected non-array messages field to be a no-op")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected bytes to pass through unmodified")
	}
}

func TestFixToolResultPairing_MissingMessagesFieldIsNoOp(t *testing.T) {
	body := []byte(`{"model":"claude-x","max_tokens":100}`)
	fixed, changed := fixToolResultPairing(body)
	if changed {
		t.Fatalf("expected a body without a messages field to be a no-op")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected bytes to pass through unmodified")
	}
}

func TestFixToolResultPairing_BackfillsOnlyTheMissingToolResult(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[
			{"type":"tool_use","id":"tu_1","name":"a","input":{}},
			{"type":"tool_use","id":"tu_2","name":"b","input":{}}
		]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"tu_1","content":"first result"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change when a tool_use has no matching tool_result")
	}

	root := decodeForAssertions(t, fixed)
	messages := messagesOf(t, root)
	if len(messages) != 2 {
		t.Fatalf("expected message count to stay at 2, got %d", len(messages))
	}
	content := contentOf(t, messages[1])
	if len(content) != 2 {
		t.Fatalf("expected the user message to end up with 2 content parts, got %d", len(content))
	}
	if partField(t, content[0], "tool_use_id") != "tu_1" {
		t.Fatalf("expected the original tool_result for tu_1 to be preserved in place")
	}
	if partField(t, content[0], "content") != "first result" {
		t.Fatalf("expected the original tool_result content to be preserved untouched")
	}
	synthetic := content[1].(map[string]interface{})
	if synthetic["tool_use_id"] != "tu_2" {
		t.Fatalf("expected a synthetic tool_result backfilled for tu_2, got %#v", synthetic)
	}
	if isErr, _ := synthetic["is_error"].(bool); !isErr {
		t.Fatalf("expected the synthetic tool_result to be flagged is_error")
	}
}

func TestFixToolResultPairing_WrapsStringContentBeforeBackfill(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]},
		{"role":"user","content":"please continue"}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change when backfilling into a string-content user message")
	}

	root := decodeForAssertions(t, fixed)
	messages := messagesOf(t, root)
	content := contentOf(t, messages[1])
	if len(content) != 2 {
		t.Fatalf("expected the string content to be wrapped and the synthetic result appended, got %d parts", len(content))
	}
	if partField(t, content[0], "type") != "text" || partField(t, content[0], "text") != "please continue" {
		t.Fatalf("expected the original string content preserved as a text block, got %#v", content[0])
	}
	if partField(t, content[1], "tool_use_id") != "tu_1" {
		t.Fatalf("expected a synthetic tool_result for tu_1, got %#v", content[1])
	}
}

func TestFixToolResultPairing_InsertsUserMessageWhenNoneFollows(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"start"},
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change when the assistant message is last in history")
	}

	root := decodeForAssertions(t, fixed)
	messages := messagesOf(t, root)
	if len(messages) != 3 {
		t.Fatalf("expected a new user message to be appended, got %d messages", len(messages))
	}
	inserted := messages[2].(map[string]interface{})
	if inserted["role"] != "user" {
		t.Fatalf("expected the inserted message to have role=user, got %#v", inserted["role"])
	}
	content := contentOf(t, inserted)
	if len(content) != 1 || partField(t, content[0], "tool_use_id") != "tu_1" {
		t.Fatalf("expected the inserted message to carry a synthetic tool_result for tu_1, got %#v", content)
	}
}

func TestFixToolResultPairing_InsertsUserMessageWhenNextIsAssistant(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]},
		{"role":"assistant","content":[{"type":"text","text":"continuing"}]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change when the next message is assistant, not user")
	}

	root := decodeForAssertions(t, fixed)
	messages := messagesOf(t, root)
	if len(messages) != 3 {
		t.Fatalf("expected an inserted user message between the two assistant messages, got %d", len(messages))
	}
	if messages[1].(map[string]interface{})["role"] != "user" {
		t.Fatalf("expected the inserted message at index 1 to have role=user")
	}
	if messages[2].(map[string]interface{})["role"] != "assistant" {
		t.Fatalf("expected the original trailing assistant message to remain at index 2")
	}
}

func TestFixToolResultPairing_ReordersOutOfOrderToolResults(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[
			{"type":"tool_use","id":"tu_1","name":"a","input":{}},
			{"type":"tool_use","id":"tu_2","name":"b","input":{}},
			{"type":"tool_use","id":"tu_3","name":"c","input":{}}
		]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"tu_3","content":"third"},
			{"type":"tool_result","tool_use_id":"tu_1","content":"first"},
			{"type":"tool_result","tool_use_id":"tu_2","content":"second"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change when tool_results are out of dispatch order")
	}

	root := decodeForAssertions(t, fixed)
	messages := messagesOf(t, root)
	content := contentOf(t, messages[1])
	if len(content) != 3 {
		t.Fatalf("expected reordering to preserve all 3 tool_results, got %d", len(content))
	}
	got := []string{
		partField(t, content[0], "tool_use_id"),
		partField(t, content[1], "tool_use_id"),
		partField(t, content[2], "tool_use_id"),
	}
	want := []string{"tu_1", "tu_2", "tu_3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected reordered ids %v, got %v", want, got)
		}
	}
}

func TestFixToolResultPairing_ReorderKeepsOtherContentBeforeToolResults(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[
			{"type":"tool_use","id":"tu_1","name":"a","input":{}},
			{"type":"tool_use","id":"tu_2","name":"b","input":{}}
		]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"tu_2","content":"second"},
			{"type":"text","text":"a note in between"},
			{"type":"tool_result","tool_use_id":"tu_1","content":"first"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change when tool_results are out of order")
	}

	root := decodeForAssertions(t, fixed)
	content := contentOf(t, messagesOf(t, root)[1])
	if len(content) != 3 {
		t.Fatalf("expected 3 content parts preserved, got %d", len(content))
	}
	if partField(t, content[0], "type") != "text" {
		t.Fatalf("expected the non-tool_result part to be moved ahead of the tool_results, got %#v", content[0])
	}
	if partField(t, content[1], "tool_use_id") != "tu_1" || partField(t, content[2], "tool_use_id") != "tu_2" {
		t.Fatalf("expected tool_results reordered to tu_1, tu_2 after the text part, got %#v", content[1:])
	}
}

func TestFixToolResultPairing_DoesNotReorderASingleToolResult(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]},
		{"role":"user","content":[
			{"type":"text","text":"note"},
			{"type":"tool_result","tool_use_id":"tu_1","content":"ok"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if changed {
		t.Fatalf("expected no change: single tool_result has nothing to reorder against and is already paired")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected pass-through bytes when unchanged")
	}
}

func TestFixToolResultPairing_DoesNotReorderWhenPrecedingToolUseCountIsOne(t *testing.T) {
	// Two tool_results are present, but only one tool_use id is known from the
	// preceding assistant message, so there is no reliable expected order to sort by.
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"tu_2","content":"orphaned result"},
			{"type":"tool_result","tool_use_id":"tu_1","content":"ok"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if changed {
		t.Fatalf("expected no reordering when fewer than 2 preceding tool_use ids are known")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected pass-through bytes when unchanged")
	}
}

func TestFixToolResultPairing_UnicodeContentSurvivesReorder(t *testing.T) {
	// This is the regression case for the goja/JS-engine surrogate-pair corruption bug:
	// content containing emoji (which encode as UTF-16 surrogate pairs) and CJK text
	// must come out byte-identical after a change forces a re-encode.
	const emojiContent = "结果 😀🎉 done 日本語"
	body := []byte(`{"messages":[
		{"role":"assistant","content":[
			{"type":"tool_use","id":"tu_1","name":"a","input":{}},
			{"type":"tool_use","id":"tu_2","name":"b","input":{}}
		]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"tu_2","content":"second"},
			{"type":"tool_result","tool_use_id":"tu_1","content":"` + jsonEscape(emojiContent) + `"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected reordering to trigger a re-encode")
	}

	root := decodeForAssertions(t, fixed)
	content := contentOf(t, messagesOf(t, root)[1])
	if partField(t, content[0], "tool_use_id") != "tu_1" {
		t.Fatalf("expected tu_1 first after reordering")
	}
	if got := partField(t, content[0], "content"); got != emojiContent {
		t.Fatalf("unicode content corrupted by re-encode: got %q, want %q", got, emojiContent)
	}
}

func TestFixToolResultPairing_UnchangedUnicodeBodyIsByteForByte(t *testing.T) {
	const emojiContent = "😀🎉 unchanged 日本語"
	body := []byte(`{"messages":[
		{"role":"user","content":"` + jsonEscape(emojiContent) + `"}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if changed {
		t.Fatalf("expected no change for a plain conversation with no tool calls")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected exact byte pass-through; any re-encoding risks corrupting unicode content")
	}
}

func TestFixToolResultPairing_PreservesLargeNumbersAcrossRewrite(t *testing.T) {
	// A number outside float64's exact-integer range (2^53) must not be perturbed
	// by the backfill rewrite, which forces json decode/re-encode of the whole body.
	body := []byte(`{"custom_id":9007199254740993,"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change: the tool_use has no following message")
	}
	if !strings.Contains(string(fixed), "9007199254740993") {
		t.Fatalf("expected the large integer literal to survive the rewrite untouched, got: %s", fixed)
	}
}

func jsonEscape(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	// json.Marshal wraps the string in quotes; strip them since callers embed
	// this into a larger hand-written JSON literal.
	return string(raw[1 : len(raw)-1])
}
