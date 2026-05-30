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
		return i18n.T("admin.role.root")
	case 10:
		return i18n.T("admin.role.admin")
	case 1:
		return i18n.T("admin.role.common")
	case 0:
		return i18n.T("admin.role.guest")
	default:
		return fmt.Sprintf("role=%d", role)
	}
}

// userStatusLabel maps the user status enum (1=enabled, 2=disabled).
func userStatusLabel(status int) string {
	switch status {
	case 1:
		return i18n.T("admin.user.status_enabled")
	case 2:
		return i18n.T("admin.user.status_disabled")
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

// printUserRows renders a user listing: id, the username padded to the
// widest in the set, then "role · status[ · quota][ · group]" with the
// email trailing when present. withQuota is off for search (its rows
// carry no quota), so search skips the extra /api/status round-trip.
func printUserRows(rows []api.AdminUserRow, perUnit float64, withQuota bool) {
	maxName := 0
	for _, u := range rows {
		if w := style.Width(u.Username); w > maxName {
			maxName = w
		}
	}
	for _, u := range rows {
		segs := []string{roleLabel(u.Role), userStatusLabel(u.Status)}
		if withQuota {
			segs = append(segs, quotaUsed(u.Quota, u.UsedQuota, perUnit))
		}
		if u.Group != "" {
			segs = append(segs, u.Group)
		}
		line := fmt.Sprintf("  #%-4d %s  %s", u.ID, padName(u.Username, maxName), strings.Join(segs, " · "))
		if u.Email != "" {
			line += "  " + u.Email
		}
		cliout.Println(line)
	}
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
	cliout.Printf("%s #%d\n", i18n.T(titleKey), id)
	for _, r := range d.rows {
		cliout.Printf("  %s  %s\n", padName(r.label+":", w+1), r.val)
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
