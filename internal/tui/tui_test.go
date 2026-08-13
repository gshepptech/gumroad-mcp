package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gshepptech/gloop/internal/gum"
)

// sized returns a model that has been through the resize the terminal would
// send on startup, which is what allocates the table.
func sized(t *testing.T) *model {
	t.Helper()
	m := newModel(context.Background(), &gum.Runner{})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

// TestRendersEveryTabWhileEmpty is the crash test: a fresh account has nothing
// in it, and every tab must still draw rather than index into a nil slice.
func TestRendersEveryTabWhileEmpty(t *testing.T) {
	m := sized(t)
	for tb := tab(0); tb < numTabs; tb++ {
		m.tab = tb
		m.loading = false
		m.rebuild()

		view := m.View()
		if view == "" {
			t.Errorf("%s rendered nothing", tb)
		}
		if !strings.Contains(view, tb.emptyHint()[:20]) {
			t.Errorf("%s did not show its empty-state hint", tb)
		}
		// Enter on an empty table must not panic or open a blank detail pane.
		m.openDetail()
		if m.inDetail {
			t.Errorf("%s opened a detail view with no rows", tb)
		}
	}
}

func TestRendersPopulatedTabs(t *testing.T) {
	m := sized(t)
	m.Update(productsMsg{[]Product{{
		ID: "P1", Name: "Pencil Icon PSD", Price: 100, Currency: "usd",
		Published: true, SalesCount: json.Number("42"), SalesUSDCents: json.Number("4200"),
		ShortURL: "https://s.gumroad.com/l/pencil", Tags: []string{"icon"},
	}}})

	view := m.View()
	for _, want := range []string{"Pencil Icon PSD", "$1.00", "live", "42"} {
		if !strings.Contains(view, want) {
			t.Errorf("products view missing %q\n%s", want, view)
		}
	}

	m.openDetail()
	if !m.inDetail {
		t.Fatal("enter did not open the detail pane")
	}
	detail := m.View()
	if !strings.Contains(detail, "P1") {
		t.Errorf("detail pane missing the product ID:\n%s", detail)
	}
	if !strings.Contains(detail, "esc back") {
		t.Errorf("detail pane missing its help line:\n%s", detail)
	}

	// Escape returns to the list.
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.inDetail {
		t.Error("esc did not leave the detail pane")
	}
}

func TestSalesAndPayoutsRender(t *testing.T) {
	m := sized(t)

	m.tab = tabSales
	m.Update(salesMsg{[]Sale{{
		ID: "S1", Email: "buyer@example.com", ProductName: "Pencil Icon PSD",
		Price: 1000, Currency: "usd", Daystamp: "5 Jan 2026 11:38 AM",
		Quantity: 1, AmountRefundable: "10",
	}}})
	if v := m.View(); !strings.Contains(v, "buyer@example.com") || !strings.Contains(v, "$10.00") {
		t.Errorf("sales view wrong:\n%s", v)
	}
	m.openDetail()
	if !strings.Contains(m.View(), "S1") {
		t.Error("sale detail missing the sale ID")
	}
	m.inDetail = false

	m.tab = tabPayouts
	id := "PO1"
	m.Update(payoutsMsg{[]Payout{{
		ID: &id, Amount: "150.00", Currency: "USD", Status: "completed",
		CreatedAt: "2026-01-05T19:38:56Z", PaymentProcessor: "stripe",
	}}})
	if v := m.View(); !strings.Contains(v, "150.00") || !strings.Contains(v, "completed") {
		t.Errorf("payouts view wrong:\n%s", v)
	}
}

// TestUpcomingPayoutHasNoID guards the nil-pointer that a payable payout
// carries: Gumroad returns id:null until the payout is processed.
func TestUpcomingPayoutHasNoID(t *testing.T) {
	m := sized(t)
	m.tab = tabPayouts
	m.Update(payoutsMsg{[]Payout{{
		Amount: "75.00", Currency: "USD", Status: "payable", CreatedAt: "2026-01-14T00:00:00Z",
	}}})
	if v := m.View(); !strings.Contains(v, "payable") {
		t.Errorf("upcoming payout did not render:\n%s", v)
	}
	m.openDetail() // must not dereference the nil ID
	if !m.inDetail {
		t.Fatal("detail did not open")
	}
	if id := m.selectedID(); id != "" {
		t.Errorf("selectedID on an upcoming payout = %q, want empty", id)
	}
}

func TestTabNavigationWraps(t *testing.T) {
	m := sized(t)
	for i := 0; i < int(numTabs); i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	}
	if m.tab != tabProducts {
		t.Errorf("tabbing through every section landed on %s, want Products", m.tab)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.tab != tabPayouts {
		t.Errorf("shift-tab from the first section landed on %s, want Payouts", m.tab)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if m.tab != tabSubscribers {
		t.Errorf("pressing 3 landed on %s, want Subscribers", m.tab)
	}
}

// TestSubscribersNeedAProductFirst covers the one navigation path a user has to
// discover: subscribers are per-product, chosen from the Products tab.
func TestSubscribersNeedAProductFirst(t *testing.T) {
	m := sized(t)
	m.tab = tabSubscribers
	m.rebuild()
	if !strings.Contains(m.View(), "Products tab") {
		t.Errorf("empty Subscribers tab does not explain how to pick a product:\n%s", m.View())
	}

	// Pick a product, then press s on it.
	m.tab = tabProducts
	m.Update(productsMsg{[]Product{{ID: "P9", Name: "Membership"}}})
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.tab != tabSubscribers {
		t.Fatalf("s from a product landed on %s", m.tab)
	}
	if m.subsProduct != "P9" {
		t.Errorf("subsProduct = %q, want P9", m.subsProduct)
	}
	if !strings.Contains(m.View(), "Membership") {
		t.Error("header does not name the product whose subscribers are shown")
	}
}

func TestErrorPaneExplainsAuthFailures(t *testing.T) {
	m := sized(t)
	m.Update(errMsg{&gum.Error{Type: "auth_error", Code: "not_authenticated", Message: "Bad token"}})
	v := m.View()
	if !strings.Contains(v, "Bad token") {
		t.Errorf("error pane lost the message:\n%s", v)
	}
	if !strings.Contains(v, "gumroad auth login") {
		t.Errorf("401 pane does not tell the user how to fix it:\n%s", v)
	}
}

func TestStripTags(t *testing.T) {
	cases := map[string]string{
		"<p>Hello, <b>world</b>!</p>":       "Hello, world!",
		"a<br>b":                            "a\nb",
		"<ul><li>one</li><li>two</li></ul>": "• one\n  • two",
		"&amp; &lt;tag&gt; &quot;q&quot;":   `& <tag> "q"`,
	}
	for in, want := range cases {
		if got := stripTags(in); got != want {
			t.Errorf("stripTags(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNarrowTerminalDoesNotPanic(t *testing.T) {
	m := newModel(context.Background(), &gum.Runner{})
	for _, size := range []tea.WindowSizeMsg{
		{Width: 20, Height: 5}, {Width: 1, Height: 1}, {Width: 200, Height: 60},
	} {
		m.Update(size)
		m.Update(productsMsg{[]Product{{ID: "P1", Name: "A very long product name indeed"}}})
		if m.View() == "" {
			t.Errorf("rendered nothing at %dx%d", size.Width, size.Height)
		}
	}
}

// TestSwitchingTabsWithLoadedRows is the regression guard for a hard crash:
// bubbles re-renders on SetColumns and walks each row's own cells while
// indexing the new column slice, so moving from the 5-column Products tab to
// any 4-column tab panicked on the stale rows.
func TestSwitchingTabsWithLoadedRows(t *testing.T) {
	m := sized(t)
	m.Update(productsMsg{[]Product{{ID: "P1", Name: "Wide row", Price: 100, Currency: "usd"}}})
	m.Update(salesMsg{[]Sale{{ID: "S1", Email: "b@e.com", ProductName: "Wide row", Price: 100}}})
	m.Update(emailsMsg{[]Email{{ID: "E1", Subject: "Hi", State: "draft"}}})
	m.Update(payoutsMsg{[]Payout{{Amount: "1.00", Currency: "USD", Status: "payable"}}})
	m.Update(subscribersMsg{[]Subscriber{{ID: "SU1", UserEmail: "s@e.com", Status: "alive"}}})

	// Every ordered pair of tabs, with rows loaded in all of them.
	for from := tab(0); from < numTabs; from++ {
		for to := tab(0); to < numTabs; to++ {
			m.tab = from
			m.rebuild()
			m.switchTab(to)
			if v := m.View(); v == "" {
				t.Fatalf("%s -> %s rendered nothing", from, to)
			}
			m.openDetail()
			m.inDetail = false
		}
	}
}

// TestDetailValuesWrapWithHangingIndent guards a layout fault: a long tag list
// or summary used to spill back to column zero, which reads as a broken pane.
func TestDetailValuesWrapWithHangingIndent(t *testing.T) {
	detailWidth = 80
	long := strings.Repeat("alpha beta gamma ", 12)
	out := field("summary", long)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("a long value did not wrap: %q", out)
	}
	for i, l := range lines {
		if len([]rune(l)) > detailWidth {
			t.Errorf("line %d is %d wide, over the %d pane: %q", i, len([]rune(l)), detailWidth, l)
		}
		if i > 0 && !strings.HasPrefix(l, strings.Repeat(" ", labelWidth+3)) {
			t.Errorf("continuation line %d is not indented under the value column: %q", i, l)
		}
	}
}

func TestWrapHandlesAnUnbreakableWord(t *testing.T) {
	lines := wrap(strings.Repeat("x", 25), 10)
	if len(lines) != 3 {
		t.Fatalf("a 25-char word at width 10 gave %d lines: %v", len(lines), lines)
	}
	for _, l := range lines {
		if len(l) > 10 {
			t.Errorf("hard cut produced an over-wide line: %q", l)
		}
	}
	if got := strings.Join(lines, ""); got != strings.Repeat("x", 25) {
		t.Errorf("wrapping lost characters: %q", got)
	}
}
