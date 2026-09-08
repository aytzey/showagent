package importer

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseChatGPTHTMLUsesOnlySelectedMappingPath(t *testing.T) {
	html := []byte(`<!doctype html><html><head><title>shell title</title></head><body>
<script id="__NEXT_DATA__" type="application/json">{
  "props":{"pageProps":{"sharedConversation":{
    "title":"Selected plan",
    "current_node":"a2",
    "mapping":{
      "root":{"id":"root","parent":null,"message":null},
      "u1":{"id":"u1","parent":"root","message":{"id":"m-u1","author":{"role":"user"},"create_time":1700000000,"content":{"content_type":"text","parts":["Question\n  code"]}}},
      "a1":{"id":"a1","parent":"u1","message":{"id":"m-a1","author":{"role":"assistant"},"content":{"parts":["Hidden branch"]}}},
      "a2":{"id":"a2","parent":"u1","message":{"id":"m-a2","author":{"role":"assistant"},"content":{"parts":["Chosen answer"]}}}
    }
  }}}
}</script></body></html>`)

	conversation, err := ParseChatGPTHTML(html)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Title != "Selected plan" || conversation.SourceKind != SourceChatGPTShare {
		t.Fatalf("metadata = %#v", conversation)
	}
	if len(conversation.Messages) != 2 || conversation.Messages[0].Text != "Question\n  code" || conversation.Messages[1].Text != "Chosen answer" {
		t.Fatalf("selected messages = %#v", conversation.Messages)
	}
	if conversation.Messages[0].SourceTimestamp == nil || conversation.Messages[0].SourceTimestamp.Unix() != 1700000000 {
		t.Fatalf("source timestamp = %#v", conversation.Messages[0].SourceTimestamp)
	}
}

func TestParseClaudeHTMLReadsExplicitMessageArrayAndSkipsNonTextBlocks(t *testing.T) {
	html := []byte(`<script type="application/json">{
  "conversation": {
    "name":"Claude plan",
    "chat_messages":[
      {"uuid":"u1","sender":"human","created_at":"2026-09-08T12:00:00Z","content":[{"type":"text","text":"Merhaba"},{"type":"attachment","name":"secret.pdf"}]},
      {"uuid":"a1","sender":"assistant","content":[{"type":"text","text":"Yanıt\n  kod"}]},
      {"uuid":"tool1","sender":"tool","content":[{"type":"text","text":"must not import"}]}
    ]
  }
}</script>`)

	conversation, err := ParseClaudeHTML(html)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Title != "Claude plan" || conversation.SourceKind != SourceClaudeShare {
		t.Fatalf("metadata = %#v", conversation)
	}
	if len(conversation.Messages) != 2 || conversation.Messages[0].Role != RoleUser || conversation.Messages[1].Text != "Yanıt\n  kod" {
		t.Fatalf("messages = %#v", conversation.Messages)
	}
	if len(conversation.Warnings) == 0 {
		t.Fatal("expected a warning for omitted non-message content")
	}
}

func TestParseClaudeHTMLPreservesTextBlockBoundaries(t *testing.T) {
	html := []byte(`<script type="application/json">{
  "chat_messages":[
    {"uuid":"u1","sender":"human","content":[
      {"type":"text","text":"Before"},
      {"type":"attachment","name":"omitted.pdf"},
      {"type":"text","text":"After"}
    ]}
  ]
}</script>`)

	conversation, err := ParseClaudeHTML(html)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation.Messages) != 1 || conversation.Messages[0].Text != "Before\nAfter" {
		t.Fatalf("message blocks = %#v, want a visible newline boundary", conversation.Messages)
	}
	if len(conversation.Warnings) == 0 {
		t.Fatal("expected an omitted-content warning")
	}
}

func TestParseShareHTMLFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{"no json", `<html><body><nav>User settings</nav><p>Assistant</p></body></html>`},
		{"unrelated json", `<script type="application/json">{"menu":{"role":"user","content":"Settings"}}</script>`},
		{"invalid json", `<script type="application/json">{"messages":[}</script>`},
		{"multiple conversations", `<script type="application/json">{"a":{"messages":[{"role":"user","content":"one"}]},"b":{"messages":[{"role":"user","content":"two"}]}}</script>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseChatGPTHTML([]byte(test.html)); !errors.Is(err, ErrConversationUnidentifiable) {
				t.Fatalf("error = %v, want ErrConversationUnidentifiable", err)
			}
		})
	}
}

func TestParseShareHTMLIgnoresUnrelatedNestedMessageArrays(t *testing.T) {
	html := []byte(`<script type="application/json">{
  "props": {
    "analytics": {
      "messages": [
        {"role":"user","content":"tracking payload"},
        {"role":"assistant","content":"not a shared conversation"}
      ]
    }
  }
}</script>`)

	if _, err := ParseChatGPTHTML(html); !errors.Is(err, ErrConversationUnidentifiable) {
		t.Fatalf("error = %v, want ErrConversationUnidentifiable", err)
	}
}

func TestParseClaudeHTMLRejectsNestedChatMessagesOutsideKnownEnvelope(t *testing.T) {
	html := []byte(`<script type="application/json">{
  "application_state": {
    "chat_messages": [
      {"sender":"human","text":"not a public snapshot"},
      {"sender":"assistant","text":"must not import"}
    ]
  }
}</script>`)

	if _, err := ParseClaudeHTML(html); !errors.Is(err, ErrConversationUnidentifiable) {
		t.Fatalf("error = %v, want ErrConversationUnidentifiable", err)
	}
}

func TestParseShareHTMLRejectsOversizeAndTooManyMessages(t *testing.T) {
	if _, err := ParseClaudeHTML(make([]byte, MaxHTMLBytes+1)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversize HTML error = %v", err)
	}

	messages := make([]Message, MaxMessages+1)
	for i := range messages {
		messages[i] = Message{Role: RoleUser, Text: "x"}
	}
	if err := validateMessages(messages); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("message limit error = %v", err)
	}
}

func TestParseChatGPTHTMLDecodesReactRouterStreamSnapshot(t *testing.T) {
	flattened := `[
  {"_1":2},"loaderData",{"_3":4},"routes/share.$shareId.($action)",{"_5":6},"serverResponse",
  {"_7":8,"_9":10,"_11":12},"title","Streamed plan","current_node","a1","mapping",
  {"_13":14,"_15":16},"u1",{"_17":-5,"_18":19},"a1",{"_17":13,"_18":20},"parent","message",
  {"_21":22,"_23":24,"_31":32},{"_21":36,"_23":37,"_31":38},"id","m1","author",{"_27":28},
  null,null,"role","user",null,null,"content",{"_33":34},"parts",[35],"Question","m2",{"_27":39},
  {"_33":40},"assistant",[41],"Answer"
]`
	quoted, err := json.Marshal(flattened)
	if err != nil {
		t.Fatal(err)
	}
	html := []byte(`<script nonce="fixture">window.__reactRouterContext.streamController.enqueue(` + string(quoted) + `)</script>`)

	conversation, err := ParseChatGPTHTML(html)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Title != "Streamed plan" || len(conversation.Messages) != 2 {
		t.Fatalf("conversation = %#v", conversation)
	}
	if conversation.Messages[0].Role != RoleUser || conversation.Messages[0].Text != "Question" ||
		conversation.Messages[1].Role != RoleAssistant || conversation.Messages[1].Text != "Answer" {
		t.Fatalf("messages = %#v", conversation.Messages)
	}
}
