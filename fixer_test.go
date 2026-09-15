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
	if partField(t, content[0], "type") != "tool_result" || partField(t, content[0], "tool_use_id") != "tu_1" {
		t.Fatalf("expected the synthetic tool_result to be placed first, got %#v", content[0])
	}
	if partField(t, content[1], "type") != "text" || partField(t, content[1], "text") != "please continue" {
		t.Fatalf("expected the original string content preserved as a trailing text block, got %#v", content[1])
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

func TestFixToolResultPairing_ReorderPlacesToolResultsBeforeOtherContent(t *testing.T) {
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
	if partField(t, content[0], "tool_use_id") != "tu_1" || partField(t, content[1], "tool_use_id") != "tu_2" {
		t.Fatalf("expected tool_results sorted to tu_1, tu_2 at the start of the message, got %#v", content[:2])
	}
	if partField(t, content[2], "type") != "text" {
		t.Fatalf("expected the non-tool_result part to be moved after all tool_results, got %#v", content[2])
	}
}

func TestFixToolResultPairing_NoChangeWhenToolResultsAlreadyFirstAndSorted(t *testing.T) {
	// A user message with tool_results first and user text at the end ("继续")
	// must NOT be mutated if tool_results are already in dispatch order.
	body := []byte(`{"messages":[
		{"role":"assistant","content":[
			{"type":"tool_use","id":"tu_1","name":"a","input":{}},
			{"type":"tool_use","id":"tu_2","name":"b","input":{}}
		]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"tu_1","content":"first"},
			{"type":"tool_result","tool_use_id":"tu_2","content":"second"},
			{"type":"text","text":"继续"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if changed {
		t.Fatalf("expected no change when tool_results are already sorted and precede trailing text")
	}
	if !bytes.Equal(fixed, body) {
		t.Fatalf("expected exact byte pass-through")
	}
}

func TestFixToolResultPairing_MovesToolResultBeforeOtherContentWhenPrepended(t *testing.T) {
	// If a text block precedes a tool_result, Anthropic rejects the turn.
	// The fixer must move the tool_result ahead of the text.
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"a","input":{}}]},
		{"role":"user","content":[
			{"type":"text","text":"note before tool_result"},
			{"type":"tool_result","tool_use_id":"tu_1","content":"ok"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected a change: text preceded the tool_result")
	}
	root := decodeForAssertions(t, fixed)
	content := contentOf(t, messagesOf(t, root)[1])
	if len(content) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(content))
	}
	if partField(t, content[0], "type") != "tool_result" || partField(t, content[0], "tool_use_id") != "tu_1" {
		t.Fatalf("expected tool_result moved to first position, got %#v", content[0])
	}
	if partField(t, content[1], "type") != "text" {
		t.Fatalf("expected text block moved after tool_result, got %#v", content[1])
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

func TestFixToolResultPairing_MergesConsecutiveUserMessagesAndPlacesResultsFirst(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[
			{"type":"tool_use","id":"cpa_gemini_1","name":"a","input":{}},
			{"type":"tool_use","id":"cpa_gemini_2","name":"b","input":{}},
			{"type":"tool_use","id":"cpa_gemini_3","name":"c","input":{}}
		]},
		{"role":"user","content":"intermediate text from user or injected hook"},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"cpa_gemini_3","content":"third"},
			{"type":"tool_result","tool_use_id":"cpa_gemini_1","content":"first"}
		]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected changed=true for consecutive user messages with missing tool_result")
	}

	root := decodeForAssertions(t, fixed)
	messages := messagesOf(t, root)
	if len(messages) != 2 {
		t.Fatalf("expected messages to be merged into 2 (assistant, user), got %d", len(messages))
	}

	userMsg := messages[1].(map[string]interface{})
	if userMsg["role"] != "user" {
		t.Fatalf("expected second message to be user")
	}

	content := userMsg["content"].([]interface{})
	if len(content) != 4 {
		t.Fatalf("expected 4 parts (3 tool_results + 1 trailing text), got %d", len(content))
	}

	expectedIDs := []string{"cpa_gemini_1", "cpa_gemini_2", "cpa_gemini_3"}
	for i, expectedID := range expectedIDs {
		part := content[i].(map[string]interface{})
		if part["type"] != "tool_result" {
			t.Fatalf("expected content[%d] to be tool_result, got %v", i, part["type"])
		}
		if part["tool_use_id"] != expectedID {
			t.Fatalf("expected content[%d] tool_use_id to be %s, got %v", i, expectedID, part["tool_use_id"])
		}
	}

	// 缺失的 cpa_gemini_2 应由 synthetic 补齐且带有 is_error
	cpaGemini2 := content[1].(map[string]interface{})
	if cpaGemini2["is_error"] != true {
		t.Fatalf("expected backfilled tool_result to have is_error=true")
	}

	// 文本内容必须排在 tool_results 之后
	lastPart := content[3].(map[string]interface{})
	if lastPart["type"] != "text" || lastPart["text"] != "intermediate text from user or injected hook" {
		t.Fatalf("expected trailing text block to follow tool_results, got: %v", lastPart)
	}
}

func TestFixToolResultPairing_MergesSystemReminderAfterToolResult(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"Bash-21","name":"Bash","input":{}}]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"Bash-21","content":"result"},
			{"type":"text","text":"user follow-up"}
		]},
		{"role":"system","content":[{"type":"text","text":"<system-reminder>continue</system-reminder>"}]}
	]}`)

	fixed, changed := fixToolResultPairing(body)
	if !changed {
		t.Fatalf("expected system reminder after tool result to be merged")
	}

	root := decodeForAssertions(t, fixed)
	messages := messagesOf(t, root)
	if len(messages) != 2 {
		t.Fatalf("expected system reminder to merge into preceding user message, got %d messages", len(messages))
	}

	userContent := messages[1].(map[string]interface{})["content"].([]interface{})
	if len(userContent) != 3 {
		t.Fatalf("expected tool result and two text blocks, got %d parts", len(userContent))
	}
	if userContent[0].(map[string]interface{})["type"] != "tool_result" {
		t.Fatalf("expected tool_result to remain first")
	}
	if userContent[2].(map[string]interface{})["text"] != "<system-reminder>continue</system-reminder>" {
		t.Fatalf("expected system reminder content to be preserved")
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
