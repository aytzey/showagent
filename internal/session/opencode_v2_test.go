package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func withFakeOpenCode2(t *testing.T, home string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
export PATH=/usr/bin:/bin
home='` + home + `'
echo "$@" >> "$home/calls.log"
case "$1" in
--version) cat "$home/version" ;;
api)
  test "$2" = GET || exit 7
  case "$3" in
  /api/session\?*)
    case "$3" in
    *cursor=*) cat "$home/page2.json" ;;
    *parentID=null*order=desc*|*order=desc*parentID=null*) cat "$home/page1.json" ;;
    *) exit 8 ;;
    esac ;;
  /api/session/*/message\?*)
    session=${3#/api/session/}
    session=${session%%/*}
    case "$3" in
    *type=user*)
      case "$3" in *order=asc*) preview=first ;; *order=desc*) preview=last ;; *) exit 9 ;; esac
      if test -f "$home/$session-$preview-error.json"; then
        cat "$home/$session-$preview-error.json"
        echo 'HTTP request failed' >&2
        exit 1
      fi
      if test -f "$home/$session-$preview.json"; then
        cat "$home/$session-$preview.json"
      else
        cat "$home/$preview.json"
      fi ;;
    *) exit 10 ;;
    esac ;;
  *) exit 11 ;;
  esac ;;
session)
  case "$2" in
  export) cat "$home/export.json" ;;
  import)
    cp "$3" "$home/imported.json"
    if test -f "$home/no-confirmation"; then exit 0; fi
    id=$(sed -n 's/.*"id":"\(ses_[0-9A-Za-z]*\)".*/\1/p' "$3" | head -n 1)
    echo "Imported session: $id" ;;
  delete) echo "Session $3 deleted" ;;
  *) exit 12 ;;
  esac ;;
*) exit 13 ;;
esac
`
	writeFile(t, filepath.Join(home, "version"), "opencode v2.2.6\n")
	writeFile(t, filepath.Join(home, "page1.json"), `{"data":[{"id":"ses_one","title":"First project","location":{"directory":"/work/one"},"time":{"created":1,"updated":2}}],"cursor":{"next":"page two/+="}}`)
	writeFile(t, filepath.Join(home, "page2.json"), `{"data":[{"id":"ses_two","title":"Second project","location":{"directory":"/work/two"},"time":{"created":3,"updated":4}}],"cursor":{}}`)
	writeFile(t, filepath.Join(home, "first.json"), `{"data":[{"id":"msg_first","type":"user","text":"first password hunter2"}],"cursor":{}}`)
	writeFile(t, filepath.Join(home, "last.json"), `{"data":[{"id":"msg_last","type":"user","text":"latest user message"}],"cursor":{}}`)
	writeFile(t, filepath.Join(home, "export.json"), `{"info":{"id":"ses_one"},"messages":[{"id":"msg_1","type":"user","text":"Hello 世界"},{"id":"msg_2","type":"synthetic","text":"injected context"},{"id":"msg_3","type":"system","text":"system context"},{"id":"msg_4","type":"assistant","content":[{"type":"reasoning","text":"private reasoning"},{"type":"text","text":"First paragraph"},{"type":"tool","state":{"content":[{"type":"text","text":"tool output"}]}},{"type":"text","text":"Second paragraph"}]},{"id":"msg_5","type":"compaction","summary":"not a user turn"},{"id":"msg_6","type":"idle"},{"id":"msg_7","type":"user","text":"  "},{"id":"msg_8","type":"user","text":"Last question"}]}`)
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

func TestOpenCode2DiscoveryAllProjectsAndPagination(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode2(t, home)
	rows := (opencodeProvider{}).Discover()
	if len(rows) != 2 {
		t.Fatalf("global discovery = %#v, want both projects", rows)
	}
	for i, want := range []string{"/work/one", "/work/two"} {
		if rows[i].CWD != want || rows[i].FirstUser != "first password [redacted]" || rows[i].LastUser != "latest user message" {
			t.Fatalf("row %d = %#v", i, rows[i])
		}
	}
	log, err := os.ReadFile(filepath.Join(home, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "cursor=page+two%2F%2B%3D") || strings.Contains(string(log), "db --format") {
		t.Fatalf("wrong v2 pagination/CLI selection: %s", log)
	}
}

func TestOpenCode2TranscriptOnlyConversationText(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode2(t, home)
	turns, err := Transcript(Row{Provider: ProviderOpenCode, ID: "ses_one"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{{Role: "user", Text: "Hello 世界"}, {Role: "assistant", Text: "First paragraph\nSecond paragraph"}, {Role: "user", Text: "Last question"}}
	if !reflect.DeepEqual(turns, want) {
		t.Fatalf("transcript = %#v, want %#v", turns, want)
	}
}

func TestOpenCodeVersionSelectsAdapter(t *testing.T) {
	for _, tt := range []struct {
		version string
		legacy  bool
		wantErr bool
	}{
		{version: "1.17.15", legacy: true},
		{version: "0.15.0", legacy: true},
		{version: "opencode v2.2.6"},
		{version: "2.0.0-beta.1"},
		{version: "opencode 2.2.6"},
		{version: "opencode v3.0.0", wantErr: true},
		{version: "notice 2.2.6 unknown", wantErr: true},
		{version: "", wantErr: true},
	} {
		t.Run(tt.version, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "version"), tt.version)
			if tt.legacy {
				withFakeOpenCode(t, home)
				writeFile(t, filepath.Join(home, "export.json"), `{"messages":[{"info":{"role":"user"},"parts":[{"type":"text","text":"legacy question"}]}]}`)
			}
			turns, err := Transcript(Row{Provider: ProviderOpenCode, ID: "ses_one"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("version %q: turns=%#v error=%v", tt.version, turns, err)
			}
			if !tt.wantErr && len(turns) == 0 {
				t.Fatal("selected adapter lost all conversation text")
			}
			if tt.wantErr {
				log, _ := os.ReadFile(filepath.Join(home, "calls.log"))
				if strings.Contains(string(log), "export") {
					t.Fatalf("unknown version attempted an export: %s", log)
				}
			}
		})
	}
}

func TestOpenCode2DiscoveryCachesAndRefreshesWAL(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode2(t, home)
	provider := opencodeProvider{}
	if rows := provider.Discover(); len(rows) != 2 {
		t.Fatalf("first listing = %#v", rows)
	}
	before, _ := os.ReadFile(filepath.Join(home, "calls.log"))
	if rows := provider.Discover(); len(rows) != 2 {
		t.Fatalf("cached listing = %#v", rows)
	}
	after, _ := os.ReadFile(filepath.Join(home, "calls.log"))
	if string(before) != string(after) {
		t.Fatal("warm cache unnecessarily invoked the CLI")
	}
	writeFile(t, filepath.Join(home, "last.json"), `{"data":[{"type":"user","text":"new message"}],"cursor":{}}`)
	writeFile(t, filepath.Join(home, "page1.json"), `{"data":[{"id":"ses_one","title":"First project","location":{"directory":"/work/one"},"time":{"created":1,"updated":5}}],"cursor":{"next":"page two/+="}}`)
	writeFile(t, filepath.Join(home, "opencode.db-wal"), "changed")
	if rows := provider.Discover(); len(rows) != 2 || rows[0].LastUser != "new message" {
		t.Fatalf("WAL change retained stale preview: %#v", rows)
	}
}

func TestOpenCode2PreviewFailuresKeepHealthySessions(t *testing.T) {
	for _, preview := range []string{"first", "last"} {
		t.Run(preview, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "ses_two-"+preview+"-error.json"), `{"_tag":"SessionNotFoundError","sessionID":"ses_two","message":"Session not found"}`)
			provider := opencodeProvider{}
			rows := provider.Discover()
			if len(rows) != 1 || rows[0].ID != "ses_one" || rows[0].LastUser != "latest user message" {
				t.Fatalf("one missing session hid healthy sessions: %#v", rows)
			}
			if err := os.Remove(filepath.Join(home, "ses_two-"+preview+"-error.json")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(home, "opencode.db-wal"), "session recovered")
			if rows := provider.Discover(); len(rows) != 2 {
				t.Fatalf("session did not recover: %#v", rows)
			}
		})
	}
}

func TestOpenCode2MalformedPreviewKeepsHealthySessions(t *testing.T) {
	for _, fixture := range []string{`not json`, `{}`, `{"data":[{"type":"synthetic","text":"injected context"}]}`} {
		t.Run(fixture, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "ses_two-first.json"), fixture)
			rows := (opencodeProvider{}).Discover()
			if len(rows) != 1 || rows[0].ID != "ses_one" {
				t.Fatalf("one corrupt preview hid healthy sessions: %#v", rows)
			}
		})
	}
}

func TestOpenCode2ProviderFailuresRemainErrors(t *testing.T) {
	for _, fixture := range []string{
		`{"_tag":"UnauthorizedError","message":"Unauthorized"}`,
		`{"_tag":"ServiceUnavailableError","message":"Service unavailable"}`,
		`{"message":"Not found"}`,
	} {
		t.Run(fixture, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "ses_two-first-error.json"), fixture)
			if _, err := (opencodeProvider{}).discoveryEntries(context.Background()); err == nil {
				t.Fatal("provider failure was treated as one missing session")
			}
		})
	}
}

func TestOpenCode2ReusesOnlyUnchangedSessionPreviews(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode2(t, home)
	provider := opencodeProvider{}
	if rows := provider.Discover(); len(rows) != 2 {
		t.Fatalf("first listing = %#v", rows)
	}
	writeFile(t, filepath.Join(home, "calls.log"), "")
	writeFile(t, filepath.Join(home, "last.json"), `{"data":[{"type":"user","text":"new password hunter2"}]}`)
	writeFile(t, filepath.Join(home, "page1.json"), `{"data":[{"id":"ses_one","title":"Renamed project","location":{"directory":"/work/one"},"time":{"created":1,"updated":5}}],"cursor":{"next":"page two/+="}}`)
	writeFile(t, filepath.Join(home, "opencode.db-wal"), "one session updated")
	rows := provider.Discover()
	if len(rows) != 2 || rows[0].LastUser != "new password [redacted]" || rows[1].LastUser != "latest user message" {
		t.Fatalf("wrong preview refresh: %#v", rows)
	}
	log, err := os.ReadFile(filepath.Join(home, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(log), "/message?") != 2 || strings.Contains(string(log), "/ses_two/message?") {
		t.Fatalf("unchanged session refetched its previews: %s", log)
	}
	cache, err := os.ReadFile(discoveryCachePath(discoveryCacheKey("opencode:db", home)))
	if err != nil || strings.Contains(string(cache), "hunter2") {
		t.Fatalf("cache leaked preview secrets: %v", err)
	}
	writeFile(t, filepath.Join(home, "calls.log"), "")
	writeFile(t, filepath.Join(home, "opencode.db-wal"), "unrelated database update")
	if rows := provider.Discover(); len(rows) != 2 {
		t.Fatalf("unrelated update lost sessions: %#v", rows)
	}
	log, _ = os.ReadFile(filepath.Join(home, "calls.log"))
	if strings.Contains(string(log), "/message?") {
		t.Fatalf("unrelated database update refetched previews: %s", log)
	}
	binary := filepath.Join(os.Getenv("PATH"), "opencode")
	script, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "calls.log"), "")
	if err := os.WriteFile(binary, append(script, '\n'), 0o755); err != nil {
		t.Fatal(err)
	}
	if rows := provider.Discover(); len(rows) != 2 || rows[1].LastUser != "new password [redacted]" {
		t.Fatalf("CLI replacement reused old previews: %#v", rows)
	}
	log, _ = os.ReadFile(filepath.Join(home, "calls.log"))
	if strings.Count(string(log), "/message?") != 4 {
		t.Fatalf("CLI replacement did not refresh all previews: %s", log)
	}
}

func TestOpenCode2RejectsMalformedAndRepeatedPages(t *testing.T) {
	for _, fixture := range []string{`not json`, `{"cursor":{}}`, `{"data":[],"cursor":{"next":"repeat"}}`} {
		t.Run(fixture, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "page1.json"), fixture)
			writeFile(t, filepath.Join(home, "page2.json"), fixture)
			if _, err := (opencodeProvider{}).discoveryEntries(context.Background()); err == nil {
				t.Fatal("invalid page accepted")
			}
		})
	}
}

func TestOpenCode2RejectsMalformedTranscriptsAndPreviews(t *testing.T) {
	for _, fixture := range []string{`not json`, `{}`, `{"messages":[{"type":"user","text":42}]}`} {
		t.Run(fixture, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "export.json"), fixture)
			if _, err := Transcript(Row{Provider: ProviderOpenCode, ID: "ses_one"}); err == nil {
				t.Fatal("malformed transcript accepted")
			}
		})
	}
	for _, fixture := range []string{`{}`, `{"data":[{"type":"synthetic","text":"injected context"}]}`} {
		t.Run(fixture, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "first.json"), fixture)
			if rows := (opencodeProvider{}).Discover(); len(rows) != 0 {
				t.Fatalf("malformed previews accepted: %#v", rows)
			}
		})
	}
}

func TestOpenCode2MalformedExportCannotWritePartialConversion(t *testing.T) {
	for _, broken := range []string{
		`{"type":"user"}`, `{"type":"user","text":null}`,
		`{"type":"assistant"}`, `{"type":"assistant","content":null}`,
		`{"type":"assistant","content":[{"type":"text"}]}`,
		`{"type":"assistant","content":[null]}`, `null`,
	} {
		t.Run(broken, func(t *testing.T) {
			root := t.TempDir()
			setEmptyHomes(t, root)
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "export.json"), `{"messages":[{"type":"user","text":"valid first question"},`+broken+`]}`)
			_, err := Convert(Row{Provider: ProviderOpenCode, ID: "ses_one", CWD: root}, ProviderClaude, HandoffOptions{})
			if err == nil {
				t.Fatal("malformed export produced a successful partial conversion")
			}
			if files, _ := filepath.Glob(filepath.Join(os.Getenv("CLAUDE_HOME"), "projects", "*", "*.jsonl")); len(files) != 0 {
				t.Fatalf("malformed export wrote destination files: %v", files)
			}
		})
	}
}

func TestOpenCode2EmptyPreviewFallsBackToTitle(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode2(t, home)
	writeFile(t, filepath.Join(home, "first.json"), `{"data":[]}`)
	writeFile(t, filepath.Join(home, "last.json"), `{"data":[]}`)
	rows := (opencodeProvider{}).Discover()
	if len(rows) != 2 || rows[0].FirstUser != "First project" || rows[0].LastUser != "" {
		t.Fatalf("empty user history = %#v", rows)
	}
}

func TestOpenCodeVersionRefreshesAfterCLIReplacement(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode2(t, home)
	provider := opencodeProvider{}
	if rows := provider.Discover(); len(rows) != 2 {
		t.Fatalf("v2 discovery = %#v", rows)
	}
	binary := filepath.Join(os.Getenv("PATH"), "opencode")
	writeFile(t, binary, "#!/bin/sh\ncase \"$1\" in\n--version) echo 1.17.15 ;;\ndb) echo '[{\"id\":\"ses_legacy\",\"created\":1,\"updated\":2,\"first_user\":\"legacy preview\"}]' ;;\nesac\n")
	if rows := provider.Discover(); len(rows) != 1 || rows[0].ID != "ses_legacy" {
		t.Fatalf("replaced CLI retained v2 cache: %#v", rows)
	}
}

func TestOpenCode2NativeImport(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode2(t, home)
	cwd := t.TempDir()
	want := []Turn{{Role: "user", Text: "Hello 世界"}, {Role: "user", Text: "Second question"}, {Role: "assistant", Text: "Answer\n  indented"}}
	row, err := (opencodeProvider{}).WriteConverted(Row{CWD: cwd}, want)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		Info struct {
			ID       string                     `json:"id"`
			Location struct{ Directory string } `json:"location"`
			Tokens   map[string]any             `json:"tokens"`
		} `json:"info"`
		Messages []struct {
			Type    string                             `json:"type"`
			Text    string                             `json:"text"`
			Content []struct{ Type, Text string }      `json:"content"`
			Time    struct{ Created, Completed int64 } `json:"time"`
			Model   struct{ ID, ProviderID string }    `json:"model"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Info.ID != row.ID || imported.Info.Location.Directory != cwd || imported.Info.Tokens == nil || len(imported.Messages) != 3 {
		t.Fatalf("invalid native session: %s", data)
	}
	for i, message := range imported.Messages {
		if message.Type != want[i].Role || message.Time.Created == 0 {
			t.Fatalf("invalid message %d: %s", i, data)
		}
		if message.Type == "user" && message.Text != want[i].Text {
			t.Fatalf("user text lost: %s", data)
		}
		if message.Type == "assistant" && (len(message.Content) != 1 || message.Content[0].Type != "text" || message.Content[0].Text != want[i].Text || message.Time.Completed == 0 || message.Model.ID == "" || message.Model.ProviderID == "") {
			t.Fatalf("invalid settled assistant: %s", data)
		}
	}
	writeFile(t, filepath.Join(home, "export.json"), string(data))
	got, err := Transcript(row)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, %v; want %#v", got, err, want)
	}
}

func TestOpenCode2ImportRejectsUnconfirmedAndUnknownVersion(t *testing.T) {
	for _, version := range []string{"opencode v2.2.6", "opencode v3.0.0"} {
		t.Run(version, func(t *testing.T) {
			home := opencodeFixtureHome(t)
			withFakeOpenCode2(t, home)
			writeFile(t, filepath.Join(home, "version"), version)
			writeFile(t, filepath.Join(home, "no-confirmation"), "")
			_, err := (opencodeProvider{}).WriteConverted(Row{CWD: t.TempDir()}, []Turn{{Role: "user", Text: "hello"}})
			if err == nil {
				t.Fatal("unverified import reported success")
			}
			if strings.Contains(version, "v3") {
				if _, err := os.Stat(filepath.Join(home, "imported.json")); !os.IsNotExist(err) {
					t.Fatal("unsupported version wrote a session")
				}
			}
		})
	}
}
