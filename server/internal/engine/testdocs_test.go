package engine_test

import "encoding/json"

// Shared decision-9 fixtures: the user-facing v1 steps artifact a version
// stores, and a compiled execution document as approval would write it.
var (
	testStepsDoc = json.RawMessage(`{"v":1,"steps":[{"id":"job","text":"Summarize last week's tickets."}]}`)

	testCompiledDoc = json.RawMessage(`{"system_prompt":"You prepare the weekly support digest.","kickoff":"Summarize last week's tickets.","provider":"anthropic","model":"claude-sonnet-5","max_tokens":2048,"compiler_v":1}`)
)
