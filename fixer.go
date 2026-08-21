package main

import (
	"bytes"
	"encoding/json"
	"sort"
)

const syntheticToolResultText = "Tool result unavailable: this tool call was not completed before the conversation history was interrupted or truncated."

// fixToolResultPairing backfills a synthetic error tool_result for any tool_use
// whose id has no matching tool_result in the immediately following message, then
// reorders tool_result blocks within a user message to match the order their
// tool_use blocks were issued in the preceding assistant message. It returns
// changed=false whenever no fix is applicable, including on malformed or
// non-tool-call-shaped bodies, so callers can leave the original bytes untouched.
func fixToolResultPairing(body []byte) (fixed []byte, changed bool) {
	if len(body) == 0 {
		return body, false
	}

	root, err := decodeJSONObject(body)
	if err != nil {
		return body, false
	}

	messages, ok := root["messages"].([]interface{})
	if !ok {
		return body, false
	}

	messages, backfilled := backfillOrphanedToolUse(messages)
	messages, reordered := reorderToolResults(messages)
	if !backfilled && !reordered {
		return body, false
	}

	root["messages"] = messages
	encoded, err := encodeJSONObject(root)
	if err != nil {
		return body, false
	}
	return encoded, true
}

func decodeJSONObject(body []byte) (map[string]interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root map[string]interface{}
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	return root, nil
}

func encodeJSONObject(root map[string]interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func buildSyntheticToolResult(id string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "tool_result",
		"tool_use_id": id,
		"is_error":    true,
		"content":     syntheticToolResultText,
	}
}

func backfillOrphanedToolUse(messages []interface{}) ([]interface{}, bool) {
	changed := false
	for i := 0; i < len(messages); i++ {
		assistantMsg, ok := messages[i].(map[string]interface{})
		if !ok || asString(assistantMsg["role"]) != "assistant" {
			continue
		}
		content, ok := assistantMsg["content"].([]interface{})
		if !ok {
			continue
		}

		var toolUseIDs []string
		for _, part := range content {
			partMap, ok := part.(map[string]interface{})
			if !ok || asString(partMap["type"]) != "tool_use" {
				continue
			}
			if id := asString(partMap["id"]); id != "" {
				toolUseIDs = append(toolUseIDs, id)
			}
		}
		if len(toolUseIDs) == 0 {
			continue
		}

		if i+1 < len(messages) {
			if nextMsg, ok := messages[i+1].(map[string]interface{}); ok && asString(nextMsg["role"]) == "user" {
				nextContent, hadArray := nextMsg["content"].([]interface{})
				if !hadArray {
					if text, ok := nextMsg["content"].(string); ok && text != "" {
						nextContent = []interface{}{map[string]interface{}{"type": "text", "text": text}}
					} else {
						nextContent = []interface{}{}
					}
				}

				existing := make(map[string]bool, len(nextContent))
				for _, part := range nextContent {
					partMap, ok := part.(map[string]interface{})
					if !ok || asString(partMap["type"]) != "tool_result" {
						continue
					}
					if id := asString(partMap["tool_use_id"]); id != "" {
						existing[id] = true
					}
				}
				for _, id := range toolUseIDs {
					if !existing[id] {
						nextContent = append(nextContent, buildSyntheticToolResult(id))
						changed = true
					}
				}
				nextMsg["content"] = nextContent
				continue
			}
		}

		syntheticContent := make([]interface{}, 0, len(toolUseIDs))
		for _, id := range toolUseIDs {
			syntheticContent = append(syntheticContent, buildSyntheticToolResult(id))
		}
		messages = insertMessageAt(messages, i+1, map[string]interface{}{
			"role":    "user",
			"content": syntheticContent,
		})
		changed = true
	}
	return messages, changed
}

func insertMessageAt(messages []interface{}, idx int, msg interface{}) []interface{} {
	out := make([]interface{}, 0, len(messages)+1)
	out = append(out, messages[:idx]...)
	out = append(out, msg)
	out = append(out, messages[idx:]...)
	return out
}

func reorderToolResults(messages []interface{}) ([]interface{}, bool) {
	changed := false
	for i, m := range messages {
		msg, ok := m.(map[string]interface{})
		if !ok || asString(msg["role"]) != "user" {
			continue
		}
		content, ok := msg["content"].([]interface{})
		if !ok || len(content) <= 1 {
			continue
		}

		var toolResults []map[string]interface{}
		var others []interface{}
		hasOthersBeforeToolResult := false
		for _, part := range content {
			if partMap, ok := part.(map[string]interface{}); ok && asString(partMap["type"]) == "tool_result" && asString(partMap["tool_use_id"]) != "" {
				if len(others) > 0 {
					hasOthersBeforeToolResult = true
				}
				toolResults = append(toolResults, partMap)
			} else {
				others = append(others, part)
			}
		}
		if len(toolResults) == 0 {
			continue
		}

		var expectedOrder []string
		for prev := i - 1; prev >= 0; prev-- {
			prevMsg, ok := messages[prev].(map[string]interface{})
			if !ok || asString(prevMsg["role"]) != "assistant" {
				continue
			}
			prevContent, ok := prevMsg["content"].([]interface{})
			if !ok {
				continue
			}
			for _, part := range prevContent {
				partMap, ok := part.(map[string]interface{})
				if !ok || asString(partMap["type"]) != "tool_use" {
					continue
				}
				if id := asString(partMap["id"]); id != "" {
					expectedOrder = append(expectedOrder, id)
				}
			}
			if len(expectedOrder) > 0 {
				break
			}
		}

		indexOf := make(map[string]int, len(expectedOrder))
		for idx, id := range expectedOrder {
			indexOf[id] = idx
		}

		needsSort := false
		if len(expectedOrder) > 1 && len(toolResults) > 1 {
			for idx := 0; idx < len(toolResults)-1; idx++ {
				if toolResultOrderIndex(toolResults[idx], indexOf) > toolResultOrderIndex(toolResults[idx+1], indexOf) {
					needsSort = true
					break
				}
			}
		}

		if !hasOthersBeforeToolResult && !needsSort {
			continue
		}

		if needsSort {
			sort.SliceStable(toolResults, func(a, b int) bool {
				return toolResultOrderIndex(toolResults[a], indexOf) < toolResultOrderIndex(toolResults[b], indexOf)
			})
		}

		// Anthropic requires tool_result blocks to appear first in the message
		// following a tool_use turn. Any trailing non-tool_result content (e.g. user
		// text or system reminders) must follow the tool_results.
		newContent := make([]interface{}, 0, len(toolResults)+len(others))
		for _, tr := range toolResults {
			newContent = append(newContent, tr)
		}
		newContent = append(newContent, others...)
		msg["content"] = newContent
		changed = true
	}
	return messages, changed
}

func toolResultOrderIndex(toolResult map[string]interface{}, indexOf map[string]int) int {
	if idx, ok := indexOf[asString(toolResult["tool_use_id"])]; ok {
		return idx
	}
	return 99999
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}
