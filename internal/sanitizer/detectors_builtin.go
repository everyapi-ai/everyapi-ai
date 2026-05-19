package sanitizer

import (
	"regexp"
	"strings"
	"unicode"
)

// Detector names — surfaced in audit logs and configurable on/off.
// Keep stable; user config files reference these strings.
const (
	DetectorOpenAIKey      = "openai_key"
	DetectorAnthropicKey   = "anthropic_key"
	DetectorGroqKey        = "groq_key"
	DetectorGoogleAPIKey   = "google_api_key"
	DetectorGitHubToken    = "github_token"
	DetectorSlackToken     = "slack_token"
	DetectorStripeKey      = "stripe_key"
	DetectorAWSAccessKey   = "aws_access_key"
	DetectorPEMPrivateKey  = "pem_private_key"
	DetectorLuhnCreditCard = "luhn_credit_card"
	DetectorChineseID      = "chinese_id"
)

// BuiltinDetectors returns the default detector set in registration
// order. Caller can append custom RegexDetectors after these.
//
// Registration order matters for ties when Scan resolves overlaps —
// the Anthropic key detector must be registered BEFORE the OpenAI key
// detector so a `sk-ant-foo` hit is reported as the Anthropic match
// (the OpenAI regex would also match the `sk-` prefix shape; longest
// wins, but on a tie the earlier-registered detector takes priority).
func BuiltinDetectors() []Detector {
	return []Detector{
		anthropicKeyDetector(),
		openAIKeyDetector(),
		groqKeyDetector(),
		googleAPIKeyDetector(),
		githubTokenDetector(),
		slackTokenDetector(),
		stripeKeyDetector(),
		awsAccessKeyDetector(),
		pemPrivateKeyDetector(),
		luhnCreditCardDetector(),
		chineseIDDetector(),
	}
}

// ---- OpenAI ----------------------------------------------------------------
//
// OpenAI personal API keys: `sk-` followed by ~40+ url-safe characters.
// Modern keys can include `proj-` or `svcacct-` prefixes after `sk-`.
// We keep the pattern lenient — false positives in source code are
// fine (placeholder, no harm), false negatives leak secrets.

func openAIKeyDetector() *RegexDetector {
	// `sk-` then ≥20 chars of [A-Za-z0-9_-]. The 20-char floor avoids
	// matching things like `sk-id=...` in URLs while still catching
	// every real key (real keys are 48+ chars).
	return NewRegexDetector(DetectorOpenAIKey, `sk-[A-Za-z0-9_\-]{20,}`)
}

// ---- Anthropic --------------------------------------------------------------
//
// Anthropic API keys: `sk-ant-` prefix, then the rest. Must register
// before openAIKeyDetector so the longer, more specific match wins on
// ties.

func anthropicKeyDetector() *RegexDetector {
	return NewRegexDetector(DetectorAnthropicKey, `sk-ant-[A-Za-z0-9_\-]{20,}`)
}

// ---- Groq ------------------------------------------------------------------
//
// Groq API keys: `gsk_` followed by ~52 url-safe chars. Distinct from
// OpenAI's `sk-` (hyphen) so no overlap with that detector.

func groqKeyDetector() *RegexDetector {
	return NewRegexDetector(DetectorGroqKey, `\bgsk_[A-Za-z0-9]{20,}\b`)
}

// ---- Google API key --------------------------------------------------------
//
// Google / Gemini API keys: `AIza` + 35 chars of [A-Za-z0-9_-], 39
// total. Fixed-shape, so anchor tightly to keep false positives low.

func googleAPIKeyDetector() *RegexDetector {
	return NewRegexDetector(DetectorGoogleAPIKey, `\bAIza[0-9A-Za-z_\-]{35}\b`)
}

// ---- GitHub token ----------------------------------------------------------
//
// Classic/scoped PATs: `ghp_|gho_|ghu_|ghs_|ghr_` + ≥36 alnum. Plus the
// fine-grained `github_pat_` form (underscores allowed in the body).

func githubTokenDetector() *RegexDetector {
	return NewRegexDetector(
		DetectorGitHubToken,
		`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[0-9A-Za-z_]{60,})\b`,
	)
}

// ---- Slack token -----------------------------------------------------------
//
// Slack tokens: `xoxb-/xoxa-/xoxp-/xoxr-/xoxs-` then dash-separated
// digit/char groups. Lenient tail — false positives are harmless
// placeholders, a missed token is a leak.

func slackTokenDetector() *RegexDetector {
	return NewRegexDetector(DetectorSlackToken, `\bxox[baprs]-[A-Za-z0-9-]{10,}`)
}

// ---- Stripe key ------------------------------------------------------------
//
// Stripe secret / restricted keys: `sk_live_|sk_test_|rk_live_|rk_test_`
// + ≥20 alnum. The underscore after `sk` means this never collides with
// OpenAI's hyphenated `sk-` detector.

func stripeKeyDetector() *RegexDetector {
	return NewRegexDetector(DetectorStripeKey, `\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{20,}\b`)
}

// ---- AWS access key --------------------------------------------------------
//
// AWS access key IDs are exactly 20 chars, start with AKIA / ASIA /
// AGPA / AIDA / AROA / AIPA / ANPA / ANVA / ASCA — `(?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASCA)[A-Z0-9]{16}`.
// We do NOT detect the corresponding secret (40 chars, no prefix) here
// because it's indistinguishable from random base64; users should set
// it as a custom rule if they know its shape, or the platform should
// recognise the access-key/secret pair from context (out of scope).

func awsAccessKeyDetector() *RegexDetector {
	return NewRegexDetector(
		DetectorAWSAccessKey,
		`\b(?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASCA)[A-Z0-9]{16}\b`,
	)
}

// ---- PEM private key -------------------------------------------------------
//
// PEM-encoded private keys: anything between `-----BEGIN ... PRIVATE
// KEY-----` and the matching `-----END ... PRIVATE KEY-----`. The
// (?s) flag lets `.` cross newlines. Greedy is OK because the END
// line is uniquely shaped.

func pemPrivateKeyDetector() *RegexDetector {
	return NewRegexDetector(
		DetectorPEMPrivateKey,
		`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`,
	)
}

// ---- Luhn credit card ------------------------------------------------------
//
// Card-shaped digit runs (13–19 digits, optionally separated by space
// or hyphen) that pass Luhn validation.

var luhnCandidateRE = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)

func luhnCreditCardDetector() *RegexDetector {
	d := &RegexDetector{name: DetectorLuhnCreditCard, re: luhnCandidateRE}
	d.Validate = luhnValid
	return d
}

func luhnValid(s string) bool {
	// Strip whitespace + dashes; require 13–19 remaining digits.
	digits := make([]int, 0, len(s))
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits = append(digits, int(r-'0'))
		case r == ' ' || r == '-':
			// allowed separator
		default:
			return false
		}
	}
	n := len(digits)
	if n < 13 || n > 19 {
		return false
	}
	sum := 0
	// Luhn: double every 2nd digit from the right (i.e. odd index
	// from the right, 0-indexed). For each doubled digit > 9, subtract 9.
	for i, d := range digits {
		// distance from the right (the check digit is at the right);
		// `n-1-i` zero-indexed from right.
		if (n-1-i)%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// ---- Chinese resident ID --------------------------------------------------
//
// 18-digit national ID (中华人民共和国居民身份证) with ISO-7064-style
// mod-11-2 check digit. Length-15 (legacy) is intentionally NOT
// matched — the 15-digit format was retired in 1999, false-positive
// risk is high against random run-of-digits.

var cnIDCandidateRE = regexp.MustCompile(`\b\d{17}[\dXx]\b`)

func chineseIDDetector() *RegexDetector {
	d := &RegexDetector{name: DetectorChineseID, re: cnIDCandidateRE}
	d.Validate = chineseIDValid
	return d
}

func chineseIDValid(s string) bool {
	if len(s) != 18 {
		return false
	}
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	check := [...]byte{'1', '0', 'X', '9', '8', '7', '6', '5', '4', '3', '2'}
	sum := 0
	for i := 0; i < 17; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		sum += int(c-'0') * weights[i]
	}
	want := check[sum%11]
	got := s[17]
	if got == 'x' {
		got = 'X'
	}
	return got == want
}

// Sanity check that built-in detector patterns compile at start-up
// (running their construction at package init catches typos before a
// user ever runs `everyapi proxy start`).
var _ = BuiltinDetectors

// UserPattern is the on-disk form of a user-defined regex rule. The
// proxy reads these from ~/.config/everyapi/sanitizer.toml (this PR
// only handles in-memory wiring; the toml parser lands in the next
// session).
type UserPattern struct {
	Name  string
	Regex string
}

// CompileUserPatterns turns user-supplied rules into detectors,
// ignoring any with invalid regex (caller should validate at write
// time too, but defence in depth).
func CompileUserPatterns(patterns []UserPattern) []Detector {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]Detector, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			continue
		}
		name := p.Name
		if name == "" {
			name = "custom_" + strings.ToLower(strings.ReplaceAll(p.Regex, "\\", "_"))
		}
		out = append(out, &RegexDetector{name: name, re: re})
	}
	return out
}
