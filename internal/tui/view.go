package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
	"github.com/gshepptech/gumroad-mcp/internal/gumroadcli"
)

var (
	pink        = lipgloss.Color("#FF90E8")
	purple      = lipgloss.Color("#7B61FF")
	green       = lipgloss.Color("#00C853")
	amber       = lipgloss.Color("#FFB300")
	red         = lipgloss.Color("#FF5252")
	grey        = lipgloss.Color("244")
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(pink)
	accentStyle = lipgloss.NewStyle().Foreground(purple)
	dimStyle    = lipgloss.NewStyle().Foreground(grey)
	okStyle     = lipgloss.NewStyle().Foreground(green)
	warnStyle   = lipgloss.NewStyle().Foreground(amber)
	errStyle    = lipgloss.NewStyle().Foreground(red)

	activeTab   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(pink).Padding(0, 1)
	inactiveTab = lipgloss.NewStyle().Foreground(grey).Padding(0, 1)

	frame = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(purple).Padding(0, 1)
)

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(grey).
		BorderBottom(true).
		Bold(true).
		Foreground(pink)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("232")).
		Background(purple).
		Bold(true)
	return s
}

func (m *model) View() string {
	if m.width == 0 {
		return "starting…"
	}

	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.tabBar())
	b.WriteString("\n\n")

	switch {
	case m.err != nil:
		b.WriteString(m.errorPane())
	case m.inDetail:
		b.WriteString(m.detail.View())
	case m.loading:
		b.WriteString(fmt.Sprintf("  %s loading %s…", m.spin.View(), strings.ToLower(m.tab.String())))
	case m.rowCount() == 0:
		b.WriteString(dimStyle.Render("  " + m.tab.emptyHint()))
	default:
		b.WriteString(m.table.View())
	}

	b.WriteString("\n\n")
	b.WriteString(m.footer())
	return frame.Width(m.contentWidth()).Render(b.String())
}

func (m *model) header() string {
	left := titleStyle.Render("gloop")
	if m.account != "" {
		left += dimStyle.Render("  ·  " + m.account)
	}
	if m.tab == tabSubscribers && m.subsProductName != "" {
		left += dimStyle.Render("  ·  " + m.subsProductName)
	}
	return left
}

func (m *model) tabBar() string {
	parts := make([]string, 0, numTabs)
	for t := tab(0); t < numTabs; t++ {
		label := fmt.Sprintf("%d %s", t+1, t)
		if t == m.tab {
			parts = append(parts, activeTab.Render(label))
		} else {
			parts = append(parts, inactiveTab.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (m *model) errorPane() string {
	var b strings.Builder
	b.WriteString(errStyle.Render("  Something went wrong") + "\n\n")
	b.WriteString("  " + m.err.Error() + "\n")

	var gerr *gumroadcli.Error
	if errors.As(m.err, &gerr) {
		switch {
		case gerr.NeedsAuth():
			b.WriteString("\n" + dimStyle.Render(
				"  The Gumroad CLI is not logged in.\n"+
					"  Press q to quit, then run: gumroad auth login"))
		case gerr.Status == 403:
			b.WriteString("\n" + dimStyle.Render(
				"  Your token lacks the scope this screen needs.\n"+
					"  Press q to quit, then run: gumroad auth login"))
		case gerr.Status == 429:
			b.WriteString("\n" + dimStyle.Render("  Gumroad is rate limiting us. Wait a moment, then press r."))
		}
	}
	b.WriteString("\n\n" + dimStyle.Render("  press r to try again"))
	return b.String()
}

func (m *model) footer() string {
	if m.inDetail {
		return dimStyle.Render("  ↑↓ scroll   esc back   q quit")
	}

	// Least important first, so a narrow terminal drops those and keeps the
	// keys a user cannot do without. Wrapping the help onto a second line reads
	// as a rendering fault, so it is trimmed to fit instead.
	optional := []string{"y show id", "r refresh"}
	if m.tab == tabProducts {
		optional = append([]string{"s subscribers"}, optional...)
	}
	essential := []string{"↑↓ move", "enter open", "tab next", "q quit"}

	keys := append(append([]string{}, optional...), essential...)
	for len(keys) > len(essential) && lipgloss.Width(strings.Join(keys, "   "))+2 > m.contentWidth() {
		keys = keys[1:]
	}

	line := dimStyle.Render("  " + strings.Join(keys, "   "))
	if m.status != "" {
		line = accentStyle.Render("  "+m.status) + "\n" + line
	}
	return line
}

func (m *model) rowCount() int {
	switch m.tab {
	case tabProducts:
		return len(m.products)
	case tabSales:
		return len(m.sales)
	case tabSubscribers:
		return len(m.subscribers)
	case tabEmails:
		return len(m.emails)
	case tabPayouts:
		return len(m.payouts)
	}
	return 0
}

// rebuild recomputes the table's columns and rows for the current tab. Column
// widths are proportional so the layout survives a resize.
func (m *model) rebuild() {
	w := m.contentWidth()
	if w < 40 {
		w = 40
	}
	share := func(f float64) int {
		n := int(float64(w) * f)
		if n < 6 {
			n = 6
		}
		return n
	}

	var cols []table.Column
	var rows []table.Row

	switch m.tab {
	case tabProducts:
		cols = []table.Column{
			{Title: "NAME", Width: share(0.34)},
			{Title: "PRICE", Width: share(0.12)},
			{Title: "STATUS", Width: share(0.12)},
			{Title: "SALES", Width: share(0.10)},
			{Title: "REVENUE", Width: share(0.14)},
		}
		for _, p := range m.products {
			rows = append(rows, table.Row{
				trunc(p.Name, share(0.34)),
				money(p.Price, p.Currency),
				productStatus(p),
				p.SalesCount.String(),
				money(atoiSafe(p.SalesUSDCents.String()), "usd"),
			})
		}

	case tabSales:
		cols = []table.Column{
			{Title: "WHEN", Width: share(0.20)},
			{Title: "PRODUCT", Width: share(0.26)},
			{Title: "BUYER", Width: share(0.26)},
			{Title: "PRICE", Width: share(0.12)},
		}
		for _, s := range m.sales {
			rows = append(rows, table.Row{
				trunc(s.Daystamp, share(0.20)),
				trunc(s.ProductName, share(0.26)),
				trunc(s.Email, share(0.26)),
				money(s.Price, s.Currency),
			})
		}

	case tabSubscribers:
		cols = []table.Column{
			{Title: "EMAIL", Width: share(0.34)},
			{Title: "PLAN", Width: share(0.14)},
			{Title: "STATUS", Width: share(0.22)},
			{Title: "SINCE", Width: share(0.16)},
		}
		for _, s := range m.subscribers {
			rows = append(rows, table.Row{
				trunc(s.UserEmail, share(0.34)),
				s.Recurrence,
				s.Status,
				trunc(s.CreatedAt, 10),
			})
		}

	case tabEmails:
		cols = []table.Column{
			{Title: "SUBJECT", Width: share(0.38)},
			{Title: "STATE", Width: share(0.14)},
			{Title: "AUDIENCE", Width: share(0.16)},
			{Title: "SENT TO", Width: share(0.12)},
		}
		for _, e := range m.emails {
			rows = append(rows, table.Row{
				trunc(e.Subject, share(0.38)),
				e.State,
				e.AudienceType,
				countOrDash(e.RecipientsCount),
			})
		}

	case tabPayouts:
		cols = []table.Column{
			{Title: "DATE", Width: share(0.18)},
			{Title: "AMOUNT", Width: share(0.18)},
			{Title: "STATUS", Width: share(0.16)},
			{Title: "VIA", Width: share(0.20)},
		}
		for _, p := range m.payouts {
			rows = append(rows, table.Row{
				trunc(p.CreatedAt, 10),
				p.Amount + " " + p.Currency,
				p.Status,
				p.PaymentProcessor,
			})
		}
	}

	// Drop the old rows BEFORE swapping columns. bubbles re-renders on
	// SetColumns, and its row renderer walks the row's own cells while indexing
	// into the new column slice — so leaving a wide tab for a narrower one
	// panics on the stale rows unless they are cleared first.
	m.table.SetRows(nil)
	m.table.SetColumns(cols)
	m.table.SetRows(rows)

	// Clamp in BOTH directions. Setting empty rows parks the cursor at -1, and
	// it does not recover on its own when rows arrive — which is every tab on
	// first paint, since the table is built empty before the fetch returns.
	if c := m.table.Cursor(); len(rows) == 0 {
		m.table.SetCursor(0)
	} else if c < 0 {
		m.table.SetCursor(0)
	} else if c >= len(rows) {
		m.table.SetCursor(len(rows) - 1)
	}
}

// openDetail renders the selected row as a full-pane record.
func (m *model) openDetail() {
	i := m.table.Cursor()
	if i < 0 || m.rowCount() == 0 {
		return
	}
	// The field renderers wrap to this, so it must match the pane in use.
	detailWidth = m.detail.Width
	if detailWidth < 40 {
		detailWidth = 40
	}

	var body string
	switch m.tab {
	case tabProducts:
		if i < len(m.products) {
			body = productDetail(m.products[i])
		}
	case tabSales:
		if i < len(m.sales) {
			body = saleDetail(m.sales[i])
		}
	case tabSubscribers:
		if i < len(m.subscribers) {
			body = subscriberDetail(m.subscribers[i])
		}
	case tabEmails:
		if i < len(m.emails) {
			body = emailDetail(m.emails[i])
		}
	case tabPayouts:
		if i < len(m.payouts) {
			body = payoutDetail(m.payouts[i])
		}
	}
	if body == "" {
		return
	}
	m.detail.SetContent(body)
	m.detail.GotoTop()
	m.inDetail = true
}

// --- detail renderers -------------------------------------------------------

// labelWidth is the detail pane's label column; values align just past it.
const labelWidth = 18

// detailWidth is the wrapping width for the pane, set when a detail is opened.
// Long values wrap with a hanging indent rather than spilling back to column
// zero, which reads as a broken layout.
var detailWidth = 80

func field(label, value string) string {
	if value == "" {
		return ""
	}
	indent := strings.Repeat(" ", labelWidth+3)
	avail := detailWidth - len(indent)
	if avail < 20 {
		avail = 20
	}

	lines := wrap(value, avail)
	var b strings.Builder
	fmt.Fprintf(&b, "  %-*s %s\n", labelWidth, dimStyle.Render(label), lines[0])
	for _, l := range lines[1:] {
		b.WriteString(indent + l + "\n")
	}
	return b.String()
}

// wrap breaks s on word boundaries, falling back to a hard cut for a single
// word longer than the line.
func wrap(s string, width int) []string {
	s = strings.ReplaceAll(s, "\n", " ")
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, w := range words {
		for len([]rune(w)) > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			r := []rune(w)
			lines = append(lines, string(r[:width]))
			w = string(r[width:])
		}
		switch {
		case cur == "":
			cur = w
		case len([]rune(cur))+1+len([]rune(w)) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func productDetail(p Product) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(p.Name) + "\n\n")
	b.WriteString(field("id", p.ID))
	b.WriteString(field("status", productStatus(p)))
	b.WriteString(field("price", money(p.Price, p.Currency)))
	b.WriteString(field("url", p.ShortURL))
	b.WriteString(field("category", p.CategoryLabel))
	b.WriteString(field("tags", strings.Join(p.Tags, ", ")))
	b.WriteString(field("sales", p.SalesCount.String()))
	b.WriteString(field("revenue", money(atoiSafe(p.SalesUSDCents.String()), "usd")))
	b.WriteString(field("summary", p.CustomSummary))
	if p.IsTieredMembership {
		b.WriteString(field("membership", strings.Join(p.Recurrences, ", ")))
	}
	b.WriteString(field("permalink", p.CustomPermalink))
	if len(p.FileInfo) > 0 {
		b.WriteString("\n" + titleStyle.Render("  Files") + "\n")
		for k, v := range p.FileInfo {
			b.WriteString(field(strings.ToLower(k), v))
		}
	}
	for _, g := range p.Variants {
		b.WriteString("\n" + titleStyle.Render("  "+g.Title) + "\n")
		for _, o := range g.Options {
			line := "  • " + o.Name
			if o.PriceDifference != nil && *o.PriceDifference != 0 {
				line += dimStyle.Render(" +" + money(*o.PriceDifference, p.Currency))
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func saleDetail(s Sale) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(s.ProductName) + "\n\n")
	b.WriteString(field("sale id", s.ID))
	b.WriteString(field("when", s.CreatedAt))
	b.WriteString(field("buyer", s.Email))
	b.WriteString(field("name", s.FullName))
	b.WriteString(field("price", money(s.Price, s.Currency)))
	b.WriteString(field("gumroad fee", money(s.GumroadFee, s.Currency)))
	b.WriteString(field("tax", money(s.TaxCents, s.Currency)))
	b.WriteString(field("refundable", s.AmountRefundable))
	b.WriteString(field("quantity", fmt.Sprint(s.Quantity)))
	b.WriteString(field("variants", s.VariantsAndQuantity))
	b.WriteString(field("license", s.LicenseKey))
	b.WriteString(field("referrer", s.Referrer))
	switch {
	case s.Refunded:
		b.WriteString(field("state", errStyle.Render("refunded")))
	case s.PartiallyRefunded:
		b.WriteString(field("state", warnStyle.Render("partially refunded")))
	case s.Disputed:
		b.WriteString(field("state", errStyle.Render("disputed")))
	}
	if len(s.CustomFields) > 0 {
		b.WriteString("\n" + titleStyle.Render("  Checkout answers") + "\n")
		for k, v := range s.CustomFields {
			b.WriteString(field(k, v))
		}
	}
	return b.String()
}

func subscriberDetail(s Subscriber) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(s.UserEmail) + "\n\n")
	b.WriteString(field("id", s.ID))
	b.WriteString(field("product", s.ProductName))
	b.WriteString(field("plan", s.Recurrence))
	b.WriteString(field("status", s.Status))
	b.WriteString(field("started", s.CreatedAt))
	b.WriteString(field("license", s.LicenseKey))
	b.WriteString(field("cancelled", strDeref(s.CancelledAt)))
	b.WriteString(field("ended", strDeref(s.EndedAt)))
	b.WriteString(field("payment failed", strDeref(s.FailedAt)))
	b.WriteString(field("free trial ends", strDeref(s.FreeTrialEndsAt)))
	return b.String()
}

func emailDetail(e Email) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(e.Subject) + "\n\n")
	b.WriteString(field("id", e.ID))
	b.WriteString(field("state", e.State))
	b.WriteString(field("audience", e.AudienceType))
	b.WriteString(field("published", strDeref(e.PublishedAt)))
	b.WriteString(field("scheduled", strDeref(e.ScheduledAt)))
	b.WriteString(field("sent to", countOrDash(e.RecipientsCount)))
	b.WriteString(field("url", strDeref(e.URL)))
	b.WriteString("\n" + titleStyle.Render("  Body") + "\n\n")
	b.WriteString(indent(stripTags(e.Message), "  "))
	return b.String()
}

func payoutDetail(p Payout) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(p.Amount+" "+p.Currency) + "\n\n")
	if p.ID != nil {
		b.WriteString(field("id", *p.ID))
	}
	b.WriteString(field("status", p.Status))
	b.WriteString(field("created", p.CreatedAt))
	b.WriteString(field("processed", strDeref(p.ProcessedAt)))
	b.WriteString(field("via", p.PaymentProcessor))
	if p.PaypalEmail != nil {
		b.WriteString(field("paypal", *p.PaypalEmail))
	}
	if p.BankAccountVisual != nil {
		b.WriteString(field("bank account", *p.BankAccountVisual))
	}
	b.WriteString(field("sales", fmt.Sprint(len(p.Sales))))
	b.WriteString(field("refunded sales", fmt.Sprint(len(p.RefundedSales))))
	b.WriteString(field("disputed sales", fmt.Sprint(len(p.DisputedSales))))
	return b.String()
}

func productStatus(p Product) string {
	switch {
	case p.Deleted:
		return errStyle.Render("deleted")
	case p.Published:
		return okStyle.Render("live")
	default:
		return warnStyle.Render("draft")
	}
}
