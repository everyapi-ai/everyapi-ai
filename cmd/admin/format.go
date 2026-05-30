package admin

// format.go centralizes the human-readable rendering shared by the admin
// list/detail views: the backend hands back raw enum integers (role,
// status) and raw quota counts, which are meaningless in a console dump,
// so these helpers map them to localized labels, convert quota to USD
// (the same divisor `everyapi auth status` uses), and pad columns by
// display width so CJK labels still line up.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/everyapi-ai/everyapi-sdk/api"

	"github.com/everyapi-ai/everyapi-ai/internal/cliout"
	"github.com/everyapi-ai/everyapi-ai/internal/i18n"
	"github.com/everyapi-ai/everyapi-ai/internal/style"
)

// roleLabel maps the backend role enum (common.RoleX: 0/1/10/100) to a
// localized word; unknown values fall back to the raw form so nothing is
// silently hidden.
func roleLabel(role int) string {
	switch role {
	case 100:
		return style.Color(i18n.T("admin.role.root"), style.ToneRed)
	case 10:
		return style.Color(i18n.T("admin.role.admin"), style.ToneYellow)
	case 1:
		return i18n.T("admin.role.common") // ordinary user — no tint
	case 0:
		return style.Color(i18n.T("admin.role.guest"), style.ToneGray)
	default:
		return fmt.Sprintf("role=%d", role)
	}
}

// userStatusLabel maps the user status enum (1=enabled, 2=disabled).
func userStatusLabel(status int) string {
	switch status {
	case 1:
		return style.Color(i18n.T("admin.user.status_enabled"), style.ToneGreen)
	case 2:
		return style.Color(i18n.T("admin.user.status_disabled"), style.ToneRed)
	default:
		return fmt.Sprintf("status=%d", status)
	}
}

// quotaPerUnit fetches the backend's quota→USD divisor once, best-effort:
// 0 means "unavailable", and fmtQuota then falls back to the raw count so
// a transient /api/status hiccup never blanks the listing.
func quotaPerUnit(c *api.Client) float64 {
	st, err := c.GetStatus(cliout.WithCtx())
	if err != nil || st.QuotaPerUnit <= 0 {
		return 0
	}
	return st.QuotaPerUnit
}

// fmtQuota renders a raw quota integer as USD when perUnit is known,
// otherwise as a thousands-separated raw count.
func fmtQuota(raw int64, perUnit float64) string {
	if perUnit > 0 {
		return fmt.Sprintf("$%.2f", float64(raw)/perUnit)
	}
	return commaInt(raw)
}

// quotaUsed renders the "$X used $Y" pair that the list/detail views show.
func quotaUsed(quota, used int64, perUnit float64) string {
	return fmt.Sprintf("%s %s %s", fmtQuota(quota, perUnit), i18n.T("admin.user.used"), fmtQuota(used, perUnit))
}

// commaInt groups an integer with thousands separators (9000000 →
// 9,000,000) for the raw-quota fallback.
func commaInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// tcol is one column of a printTable: a header plus whether it
// right-aligns (numbers read better flush-right).
type tcol struct {
	header string
	right  bool
}

// printTable renders an aligned table: a header row, then each row's cells
// padded to the widest cell in their column (display width, CJK-safe), with
// two-space gutters. trailing[i], when non-empty, is appended after the
// fixed columns for row i — used for the often-empty, often-long email so
// it never widens every row.
func printTable(cols []tcol, rows [][]string, trailing []string) {
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = style.Width(c.header)
	}
	for _, r := range rows {
		for i, cell := range r {
			if x := style.Width(cell); x > w[i] {
				w[i] = x
			}
		}
	}
	put := func(cells []string, trail string) {
		var b strings.Builder
		b.WriteString("  ")
		for i, cell := range cells {
			if i > 0 {
				b.WriteString("  ")
			}
			if cols[i].right {
				b.WriteString(padLeft(cell, w[i]))
			} else {
				b.WriteString(padName(cell, w[i]))
			}
		}
		if trail != "" {
			b.WriteString("  " + trail)
		}
		cliout.Println(strings.TrimRight(b.String(), " "))
	}
	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = style.Dim(c.header) // de-emphasize the header row
	}
	put(header, "")
	for i, r := range rows {
		t := ""
		if trailing != nil {
			t = trailing[i]
		}
		put(r, t)
	}
}

// printUserRows renders a user listing as an aligned table. withQuota is
// off for search (its rows carry no quota), so it drops the quota/used
// columns and skips the extra /api/status round-trip.
func printUserRows(rows []api.AdminUserRow, perUnit float64, withQuota bool) {
	cols := []tcol{
		{"ID", true},
		{i18n.T("admin.user.f_username"), false},
		{i18n.T("admin.user.f_role"), false},
		{i18n.T("admin.user.f_status"), false},
	}
	if withQuota {
		cols = append(cols, tcol{i18n.T("admin.user.f_quota"), true}, tcol{i18n.T("admin.user.used"), true})
	}
	cols = append(cols, tcol{i18n.T("admin.user.f_group"), false})

	cells := make([][]string, len(rows))
	trailing := make([]string, len(rows))
	for i, u := range rows {
		row := []string{fmt.Sprintf("#%d", u.ID), u.Username, roleLabel(u.Role), userStatusLabel(u.Status)}
		if withQuota {
			row = append(row, fmtQuota(u.Quota, perUnit), fmtQuota(u.UsedQuota, perUnit))
		}
		row = append(row, u.Group)
		cells[i] = row
		trailing[i] = u.Email
	}
	printTable(cols, cells, trailing)
}

// detail accumulates "label: value" rows for a single-record view. Build
// it with add (which skips empty values), then hand it to printDetail —
// shared by every admin detail view so they align and read identically.
type detail struct {
	rows []struct{ label, val string }
}

// add appends a row unless val is empty (so optional fields just vanish).
func (d *detail) add(labelKey, val string) {
	if val != "" {
		d.rows = append(d.rows, struct{ label, val string }{i18n.T(labelKey), val})
	}
}

// printDetail prints "<title> #<id>" then the accumulated rows as aligned
// "label: value" pairs, label column padded by display width (CJK-safe).
func printDetail(titleKey string, id int, d detail) {
	w := 0
	for _, r := range d.rows {
		if x := style.Width(r.label); x > w {
			w = x
		}
	}
	cliout.Printf("%s #%d\n", style.Bold(i18n.T(titleKey)), id)
	for _, r := range d.rows {
		// Dim the label column (same de-emphasis as table headers); the
		// value keeps whatever tint roleLabel/statusLabel gave it.
		cliout.Printf("  %s  %s\n", style.Dim(padName(r.label+":", w+1)), r.val)
	}
}

// okText / mutedText tint a localized message: green for a successful
// mutation, gray for a no-op / cancellation. The whole template is
// wrapped, so any %-verb inside is filled by the caller's Printf after
// the color codes — and both are TTY-aware (plain when unstyled).
func okText(key string) string    { return style.Color(i18n.T(key), style.ToneGreen) }
func mutedText(key string) string { return style.Color(i18n.T(key), style.ToneGray) }

// boolState tints a marketplace.enabled flag value: true → green, false →
// gray, anything else (e.g. "<unset>") dimmed.
func boolState(s string) string {
	switch s {
	case "true":
		return style.Color(s, style.ToneGreen)
	case "false":
		return style.Color(s, style.ToneGray)
	default:
		return style.Dim(s)
	}
}

// printUserDetail renders the single-user view.
func printUserDetail(u *api.AdminUserRow, perUnit float64) {
	var d detail
	d.add("admin.user.f_username", u.Username)
	d.add("admin.user.f_email", u.Email)
	d.add("admin.user.f_role", roleLabel(u.Role))
	d.add("admin.user.f_status", userStatusLabel(u.Status))
	d.add("admin.user.f_group", u.Group)
	d.add("admin.user.f_quota", quotaUsed(u.Quota, u.UsedQuota, perUnit))
	d.add("admin.user.f_display", u.DisplayName)
	printDetail("admin.user.detail_title", u.ID, d)
}

// padName right-pads s to w display columns (CJK-aware) for the one
// variable-width column the user list aligns on.
func padName(s string, w int) string {
	if d := w - style.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// padLeft left-pads s to w display columns (right-alignment) for numeric
// columns so decimal points and ids line up.
func padLeft(s string, w int) string {
	if d := w - style.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}
