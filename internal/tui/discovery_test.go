package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	list "charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/aytzey/showagent/internal/session"
)

func TestDiscoveryShowsReadySessionsBeforeOtherProvidersFinish(t *testing.T) {
	m := newLoadingModel()
	m.width, m.height = 120, 36
	row := session.Row{Provider: session.ProviderClaude, ID: "ready", CWD: "/work/ready", FirstUser: "ready to resume", LastAt: time.Now()}
	more := make(chan session.ProviderSessions)
	updated, cmd := m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: row.Provider, Rows: []session.Row{row}}, more: more})
	got := asModel(t, updated)
	if got.loading || len(got.allRows) != 1 {
		t.Fatalf("a ready provider is still hidden behind the loading screen: loading=%v rows=%d", got.loading, len(got.allRows))
	}
	if cmd == nil {
		t.Fatal("partial discovery stopped listening for remaining providers")
	}
	if view := got.View().Content; !strings.Contains(view, "ready to resume") || !strings.Contains(view, "scanning") {
		t.Fatalf("partial discovery did not show the usable row and progress: %s", view)
	}
}

func TestDiscoveryStartsOnNewestSessionRegardlessOfProviderOrder(t *testing.T) {
	now := time.Now()
	old := session.Row{Provider: session.ProviderClaude, ID: "old", CWD: "/work/old", LastAt: now.Add(-time.Hour)}
	newer := make([]session.Row, 45)
	for i := range newer {
		newer[i] = session.Row{Provider: session.ProviderCodex, ID: fmt.Sprintf("new-%d", i), CWD: fmt.Sprintf("/work/new-%d", i), LastAt: now.Add(-time.Duration(i) * time.Minute)}
	}
	oldProvider := session.ProviderSessions{Provider: old.Provider, Rows: []session.Row{old}}
	newerProvider := session.ProviderSessions{Provider: session.ProviderCodex, Rows: newer}
	for name, providers := range map[string][]session.ProviderSessions{
		"newest arrives last":  {oldProvider, newerProvider},
		"newest arrives first": {newerProvider, oldProvider},
	} {
		t.Run(name, func(t *testing.T) {
			m := newLoadingModel()
			m.width, m.height = 100, 30
			more := make(chan session.ProviderSessions)
			for _, provider := range providers {
				updated, _ := m.Update(providerSessionsMsg{rows: provider, more: more})
				m = asModel(t, updated)
			}
			updated, _ := m.Update(providerSessionsMsg{done: true})
			m = asModel(t, updated)
			selected, ok := m.list.SelectedItem().(item)
			if !ok || selected.row.ID != newer[0].ID || m.list.Paginator.Page != 0 {
				t.Fatalf("startup landed on an old session/page: selected=%#v page=%d; want %q on page 0", m.list.SelectedItem(), m.list.Paginator.Page, newer[0].ID)
			}
		})
	}
}

func TestLateProviderKeepsKeyboardSelectionDuringStartup(t *testing.T) {
	now := time.Now()
	rows := []session.Row{
		{Provider: session.ProviderClaude, ID: "first", CWD: "/work", LastAt: now},
		{Provider: session.ProviderClaude, ID: "chosen", CWD: "/work", LastAt: now.Add(-time.Hour)},
	}
	m := newLoadingModel()
	more := make(chan session.ProviderSessions)
	updated, _ := m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: session.ProviderClaude, Rows: rows}, more: more})
	m = asModel(t, updated)
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = asModel(t, updated)
	later := session.Row{Provider: session.ProviderCodex, ID: "newest", CWD: "/other", LastAt: now.Add(time.Hour)}
	updated, _ = m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: later.Provider, Rows: []session.Row{later}}, more: more})
	m = asModel(t, updated)
	updated, _ = m.Update(providerSessionsMsg{done: true})
	m = asModel(t, updated)
	selected, ok := m.list.SelectedItem().(item)
	if !ok || selected.row.ID != "chosen" {
		t.Fatalf("background discovery moved the user's keyboard selection: %#v", m.list.SelectedItem())
	}
}

func TestLateProviderKeepsSearchSelectionAndInFlightAction(t *testing.T) {
	now := time.Now()
	first := session.Row{Provider: session.ProviderClaude, ID: "first", CWD: "/work/first", FirstUser: "fix login", LastAt: now}
	chosen := session.Row{Provider: session.ProviderClaude, ID: "chosen", CWD: "/work/chosen", FirstUser: "fix login again", LastAt: now.Add(-time.Hour)}
	m := newLoadingModel()
	m.width, m.height = 120, 36
	more := make(chan session.ProviderSessions)
	updated, _ := m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: first.Provider, Rows: []session.Row{first, chosen}}, more: more})
	m = asModel(t, updated)
	searching, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/'}))
	m = asModel(t, searching)
	m.list.SetFilterText("login")
	selectRowItem(&m.list, chosen)
	m.busy = "conversion"
	m.pendingConvert = &pendingConversion{rowKey: rowKey(chosen)}
	later := session.Row{Provider: session.ProviderCodex, ID: "later", CWD: "/work/later", FirstUser: "fix login elsewhere", LastAt: now.Add(time.Hour)}
	updated, _ = m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: later.Provider, Rows: []session.Row{later}}, more: more})
	m = asModel(t, updated)
	if m.list.FilterValue() != "login" || len(m.list.VisibleItems()) != 3 {
		t.Fatalf("late provider lost the current search: query=%q results=%d", m.list.FilterValue(), len(m.list.VisibleItems()))
	}
	selected, ok := m.list.SelectedItem().(item)
	if !ok || selected.row.ID != chosen.ID {
		t.Fatalf("late provider moved the cursor away from the chosen session: %#v", m.list.SelectedItem())
	}
	// An older asynchronous search result must not replace the refreshed list.
	updated, _ = m.Update(list.FilterMatchesMsg{})
	m = asModel(t, updated)
	if len(m.list.VisibleItems()) != 3 {
		t.Fatal("a delayed filter result discarded sessions from a completed provider")
	}
	updated, _ = m.Update(providerSessionsMsg{done: true})
	m = asModel(t, updated)
	selected, ok = m.list.SelectedItem().(item)
	if !ok || selected.row.ID != chosen.ID || m.busy != "conversion" || m.pendingConvert == nil || m.discovering {
		t.Fatalf("finishing background discovery disturbed the current action: selected=%#v busy=%q pending=%v scanning=%v", selected, m.busy, m.pendingConvert != nil, m.discovering)
	}
}

func TestProgressiveRescanRemovesDeletedRowsAndKeepsHiddenProviders(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "keep", CWD: "/work", LastAt: time.Now()},
		{Provider: session.ProviderCodex, ID: "gone", CWD: "/work"},
		{Provider: session.ProviderClaude, ID: "hidden", CWD: "/work/claude"},
	}
	m := sizedModel(rows)
	m.providers[session.ProviderClaude] = false
	m.rescanning, m.discovering, m.busy = true, true, "rescan"
	more := make(chan session.ProviderSessions)
	updated, _ := m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: session.ProviderCodex, Rows: rows[:1]}, more: more})
	m = asModel(t, updated)
	if len(m.allRows) != 2 || m.providers[session.ProviderClaude] || m.busy != "rescan" {
		t.Fatalf("rescan did not replace its completed provider safely: rows=%d hidden=%v busy=%q", len(m.allRows), !m.providers[session.ProviderClaude], m.busy)
	}
	updated, _ = m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: session.ProviderClaude}, more: more})
	updated, _ = asModel(t, updated).Update(providerSessionsMsg{done: true})
	m = asModel(t, updated)
	if len(m.allRows) != 1 || m.rescanning || m.busy != "" || m.loading {
		t.Fatalf("progressive rescan did not complete: rows=%d scanning=%v busy=%q", len(m.allRows), m.rescanning, m.busy)
	}
}

func TestProviderShortcutsDoNotMoveAsSourcesArrive(t *testing.T) {
	m := newLoadingModel()
	more := make(chan session.ProviderSessions)
	for _, provider := range []session.Provider{session.ProviderClaude, session.ProviderPi} {
		updated, _ := m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: provider, Rows: []session.Row{{Provider: provider, ID: string(provider)}}}, more: more})
		m = asModel(t, updated)
	}
	if label, _, _ := m.providerFilterHelp(); label != "2/7" {
		t.Fatalf("ready sources do not have their stable provider keys: %q", label)
	}
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '7'}))
	m = asModel(t, updated)
	if m.providers[session.ProviderPi] {
		t.Fatal("key 7 did not hide Pi before Codex finished scanning")
	}
	updated, _ = m.Update(providerSessionsMsg{rows: session.ProviderSessions{Provider: session.ProviderCodex, Rows: []session.Row{{Provider: session.ProviderCodex, ID: "late"}}}, more: more})
	m = asModel(t, updated)
	updated, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '7'}))
	m = asModel(t, updated)
	if !m.providers[session.ProviderPi] || !m.providers[session.ProviderClaude] {
		t.Fatal("a late provider changed the meaning of an existing filter shortcut")
	}
}

func TestClosedPickerDoesNotStartALateDiscoveryCommand(t *testing.T) {
	controller := &discoveryController{}
	cmd := loadSessionsCmd(controller)
	controller.close()
	msg, ok := cmd().(providerSessionsMsg)
	if !ok || !msg.done || controller.current != nil {
		t.Fatal("a command scheduled before quit started another provider scan")
	}
}
