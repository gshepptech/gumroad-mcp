// Package tui is gloop's interactive terminal interface — the face of the tool
// for people who would rather not memorise flags.
package tui

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gshepptech/gloop/internal/gum"
)

// Run starts the interactive interface and blocks until the user quits.
func Run(ctx context.Context, api *gum.Runner) error {
	m := newModel(ctx, api)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// --- tabs -------------------------------------------------------------------

type tab int

const (
	tabProducts tab = iota
	tabSales
	tabSubscribers
	tabEmails
	tabPayouts
	numTabs
)

func (t tab) String() string {
	return [...]string{"Products", "Sales", "Subscribers", "Emails", "Payouts"}[t]
}

// emptyHint tells the user what to do when a tab has nothing in it, rather than
// leaving them staring at a blank pane.
func (t tab) emptyHint() string {
	switch t {
	case tabProducts:
		return "No products yet. Create one at gumroad.com, or run:\n  gumroad products create --name \"My product\" --price 9.99"
	case tabSales:
		return "No sales in this account yet."
	case tabSubscribers:
		return "Pick a membership product on the Products tab and press s to see its subscribers."
	case tabEmails:
		return "No audience emails yet. Draft one with:\n  gumroad emails create --subject \"Hello\" --body ./email.html"
	case tabPayouts:
		return "No payouts yet. Gumroad pays out once you have a balance and a payout method set."
	}
	return "Nothing here."
}

// --- model ------------------------------------------------------------------

type model struct {
	ctx context.Context
	api *gum.Runner

	tab     tab
	width   int
	height  int
	loading bool
	err     error
	status  string

	spin  spinner.Model
	table table.Model

	// detail is non-empty when the user has drilled into a row.
	detail    viewport.Model
	inDetail  bool
	detailTtl string

	account string

	products    []Product
	sales       []Sale
	subscribers []Subscriber
	emails      []Email
	payouts     []Payout

	// subsProduct is the product whose subscribers are shown on the
	// Subscribers tab, chosen from the Products tab with `s`.
	subsProduct     string
	subsProductName string

	loaded [numTabs]bool
}

func newModel(ctx context.Context, api *gum.Runner) *model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = accentStyle

	t := table.New(table.WithFocused(true))
	t.SetStyles(tableStyles())

	return &model{
		ctx:    ctx,
		api:    api,
		spin:   s,
		table:  t,
		detail: viewport.New(0, 0),
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.loadAccount(), m.load(m.tab))
}

// --- messages ---------------------------------------------------------------

type (
	accountMsg     struct{ name string }
	productsMsg    struct{ items []Product }
	salesMsg       struct{ items []Sale }
	subscribersMsg struct{ items []Subscriber }
	emailsMsg      struct{ items []Email }
	payoutsMsg     struct{ items []Payout }
	errMsg         struct{ err error }
	statusMsg      struct{ text string }
)

func (m *model) loadAccount() tea.Cmd {
	return func() tea.Msg {
		acct, err := fetchAccount(m.ctx, m.api)
		if err != nil {
			return errMsg{err}
		}
		name := acct.DisplayName
		if name == "" {
			name = acct.Name
		}
		if name == "" {
			name = acct.Email
		}
		return accountMsg{name}
	}
}

// load returns the command that fetches one tab's data. Each shells out to the
// official CLI, so the TUI shows exactly what `gumroad ... --json` reports.
func (m *model) load(t tab) tea.Cmd {
	m.loading = true
	m.err = nil

	// Captured so the closure does not read model state from another goroutine.
	ctx, api := m.ctx, m.api

	switch t {
	case tabProducts:
		return func() tea.Msg {
			v, err := fetchProducts(ctx, api)
			if err != nil {
				return errMsg{err}
			}
			return productsMsg{v}
		}
	case tabSales:
		return func() tea.Msg {
			v, err := fetchSales(ctx, api)
			if err != nil {
				return errMsg{err}
			}
			return salesMsg{v}
		}
	case tabEmails:
		return func() tea.Msg {
			v, err := fetchEmails(ctx, api)
			if err != nil {
				return errMsg{err}
			}
			return emailsMsg{v}
		}
	case tabPayouts:
		return func() tea.Msg {
			v, err := fetchPayouts(ctx, api)
			if err != nil {
				return errMsg{err}
			}
			return payoutsMsg{v}
		}
	case tabSubscribers:
		// Subscribers are per-product; the user picks one with `s` on the
		// Products tab. Until then there is nothing to fetch.
		if m.subsProduct == "" {
			m.loading = false
			return func() tea.Msg { return subscribersMsg{nil} }
		}
		pid := m.subsProduct
		return func() tea.Msg {
			v, err := fetchSubscribers(ctx, api, pid)
			if err != nil {
				return errMsg{err}
			}
			return subscribersMsg{v}
		}
	}
	m.loading = false
	return nil
}

// --- update -----------------------------------------------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case accountMsg:
		m.account = msg.name
		return m, nil

	case errMsg:
		m.loading = false
		m.err = msg.err
		return m, nil

	case statusMsg:
		m.status = msg.text
		return m, nil

	case productsMsg:
		m.loading = false
		m.products = msg.items
		m.loaded[tabProducts] = true
		m.rebuild()
		return m, nil

	case salesMsg:
		m.loading = false
		m.sales = msg.items
		m.loaded[tabSales] = true
		m.rebuild()
		return m, nil

	case subscribersMsg:
		m.loading = false
		m.subscribers = msg.items
		m.loaded[tabSubscribers] = true
		m.rebuild()
		return m, nil

	case emailsMsg:
		m.loading = false
		m.emails = msg.items
		m.loaded[tabEmails] = true
		m.rebuild()
		return m, nil

	case payoutsMsg:
		m.loading = false
		m.payouts = msg.items
		m.loaded[tabPayouts] = true
		m.rebuild()
		return m, nil
	}

	var cmd tea.Cmd
	if m.inDetail {
		m.detail, cmd = m.detail.Update(msg)
	} else {
		m.table, cmd = m.table.Update(msg)
	}
	return m, cmd
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Detail view has its own, smaller keymap.
	if m.inDetail {
		switch msg.String() {
		case "esc", "q", "backspace", "left":
			m.inDetail = false
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "tab", "right", "l":
		m.switchTab((m.tab + 1) % numTabs)
		return m, m.maybeLoad()

	case "shift+tab", "left", "h":
		m.switchTab((m.tab + numTabs - 1) % numTabs)
		return m, m.maybeLoad()

	case "1", "2", "3", "4", "5":
		m.switchTab(tab(msg.String()[0] - '1'))
		return m, m.maybeLoad()

	case "r":
		m.status = ""
		return m, m.load(m.tab)

	case "enter":
		m.openDetail()
		return m, nil

	case "s":
		// From a product row, jump to its subscribers.
		if m.tab == tabProducts {
			if p := m.selectedProduct(); p != nil {
				m.subsProduct, m.subsProductName = p.ID, p.Name
				m.loaded[tabSubscribers] = false
				m.switchTab(tabSubscribers)
				return m, m.load(tabSubscribers)
			}
		}
		return m, nil

	case "y":
		// Copy-friendly: surface the selected row's ID in the status bar.
		if id := m.selectedID(); id != "" {
			m.status = "id: " + id
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *model) switchTab(t tab) {
	m.tab = t
	m.status = ""
	m.err = nil
	m.rebuild()
}

// maybeLoad fetches a tab's data the first time it is opened.
func (m *model) maybeLoad() tea.Cmd {
	if m.loaded[m.tab] {
		return nil
	}
	return m.load(m.tab)
}

func (m *model) selectedProduct() *Product {
	if m.tab != tabProducts {
		return nil
	}
	i := m.table.Cursor()
	if i < 0 || i >= len(m.products) {
		return nil
	}
	return &m.products[i]
}

func (m *model) selectedID() string {
	i := m.table.Cursor()
	if i < 0 {
		return ""
	}
	switch m.tab {
	case tabProducts:
		if i < len(m.products) {
			return m.products[i].ID
		}
	case tabSales:
		if i < len(m.sales) {
			return m.sales[i].ID
		}
	case tabSubscribers:
		if i < len(m.subscribers) {
			return m.subscribers[i].ID
		}
	case tabEmails:
		if i < len(m.emails) {
			return m.emails[i].ID
		}
	case tabPayouts:
		if i < len(m.payouts) && m.payouts[i].ID != nil {
			return *m.payouts[i].ID
		}
	}
	return ""
}

func (m *model) resize() {
	// Chrome: 1 title + 1 tab bar + 1 rule + 2 footer lines + borders.
	const chrome = 8
	h := m.height - chrome
	if h < 3 {
		h = 3
	}
	m.table.SetHeight(h)
	m.detail.Width = m.width - 4
	m.detail.Height = h + 1
	m.rebuild()
}

func (m *model) contentWidth() int {
	w := m.width - 4
	if w < 40 {
		w = 40
	}
	return w
}
