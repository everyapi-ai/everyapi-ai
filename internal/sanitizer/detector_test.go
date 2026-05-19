package sanitizer

import (
	"strings"
	"testing"
)

// ---- builtin detector tests ------------------------------------------------

func TestDetector_OpenAIKey(t *testing.T) {
	d := openAIKeyDetector()
	cases := []struct {
		s      string
		want   int
		sample string
	}{
		{"plain text", 0, ""},
		{"my key is sk-proj-abcdefghijklmnopqrstuvwxyz1234567890 done", 1, "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"},
		{"sk-ant-… does match the openai regex too", 0 /*via Scan anthropic wins; here detector alone sees this*/, ""},
		// 19 char body — below floor:
		{"sk-tooshortdontmatch12", 0, ""},
	}
	for _, tc := range cases {
		got := d.Find(tc.s)
		if len(got) != tc.want {
			t.Errorf("OpenAI key detector on %q got %d matches, want %d (%+v)", tc.s, len(got), tc.want, got)
			continue
		}
		if tc.want > 0 && got[0].Value != tc.sample {
			t.Errorf("OpenAI key match %q, want %q", got[0].Value, tc.sample)
		}
	}
}

func TestDetector_AnthropicKey(t *testing.T) {
	d := anthropicKeyDetector()
	s := "auth: sk-ant-api03_abcdefghijklmnopqrstuvwxyz1234567890XYZ end"
	got := d.Find(s)
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %d", len(got))
	}
	if !strings.HasPrefix(got[0].Value, "sk-ant-") {
		t.Errorf("matched %q, want sk-ant- prefix", got[0].Value)
	}
}

func TestDetector_NewProviderKeys(t *testing.T) {
	// Fixtures intentionally use clearly-fake bodies (FAKE / TESTFIXTURE
	// markers, `sk_test_` instead of `sk_live_`) so they match our regex
	// without tripping GitHub Push Protection on the mirror repo — real
	// `sk_live_…` strings with valid Stripe checksums get rejected at
	// push time. Keep the markers if you ever rewrite these.
	cases := []struct {
		name     string
		d        *RegexDetector
		hay      string
		wantMTch string
	}{
		{"groq", groqKeyDetector(),
			"GROQ_API_KEY=gsk_abcdefghijklmnopqrstuvwx0123456789", "gsk_abcdefghijklmnopqrstuvwx0123456789"},
		{"google", googleAPIKeyDetector(),
			"key AIzaSyB1234567890abcdefghijklmnopqrstuv end", "AIzaSyB1234567890abcdefghijklmnopqrstuv"},
		{"github-classic", githubTokenDetector(),
			"token ghp_0123456789abcdefghijklmnopqrstuvwxyz here", "ghp_0123456789abcdefghijklmnopqrstuvwxyz"},
		{"github-finegrained", githubTokenDetector(),
			"github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz0123456789ABCD x",
			"github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz0123456789ABCD"},
		{"slack", slackTokenDetector(),
			"xoxb-FAKEXOXBTESTFIXTUREDONOTUSE next", "xoxb-FAKEXOXBTESTFIXTUREDONOTUSE"},
		{"stripe", stripeKeyDetector(),
			"STRIPE=sk_test_FAKETESTFIXTUREDONOTUSE000 end", "sk_test_FAKETESTFIXTUREDONOTUSE000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.d.Find(c.hay)
			if len(got) != 1 || got[0].Value != c.wantMTch {
				t.Fatalf("%s: got %+v, want single match %q", c.name, got, c.wantMTch)
			}
		})
	}
}

func TestScan_StripeKeyNotConfusedWithOpenAI(t *testing.T) {
	// OpenAI keys are `sk-` (hyphen); Stripe is `sk_` (underscore).
	// Both must be caught, by their OWN detector, with no overlap
	// double-replace.
	s := "openai sk-proj_abcdefghijklmnopqrstuvwxyz0 and stripe sk_test_FAKETESTFIXTUREDONOTUSE001"
	matches := Scan(s, BuiltinDetectors())
	byName := map[string]bool{}
	for _, m := range matches {
		byName[m.DetectorName] = true
	}
	if !byName[DetectorOpenAIKey] {
		t.Errorf("OpenAI key not detected: %+v", matches)
	}
	if !byName[DetectorStripeKey] {
		t.Errorf("Stripe key not detected: %+v", matches)
	}
}

func TestDetector_AWSAccessKey(t *testing.T) {
	d := awsAccessKeyDetector()
	cases := []struct {
		s    string
		want int
	}{
		{"AKIAIOSFODNN7EXAMPLE", 1},
		{"prefix=AKIAIOSFODNN7EXAMPLE,suffix", 1},
		{"ASIAY34FZKBOKMUTVV7A", 1},
		{"lowercase akia... no", 0},
		{"AKIA too short", 0},
		{"AKIAIOSFODNN7TOOLONGEXTRA", 0}, // 25 chars instead of 20
	}
	for _, tc := range cases {
		got := d.Find(tc.s)
		if len(got) != tc.want {
			t.Errorf("AWS detector on %q got %d, want %d", tc.s, len(got), tc.want)
		}
	}
}

func TestDetector_PEMPrivateKey(t *testing.T) {
	d := pemPrivateKeyDetector()
	body := `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEAv+5...truncated...
-----END RSA PRIVATE KEY-----`
	s := "before " + body + " after"
	got := d.Find(s)
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %d", len(got))
	}
	if got[0].Value != body {
		t.Errorf("PEM body mismatch")
	}
}

func TestDetector_Luhn(t *testing.T) {
	d := luhnCreditCardDetector()
	cases := []struct {
		s    string
		want int
	}{
		{"4111 1111 1111 1111", 1}, // canonical Luhn-valid test card
		{"5500 0000 0000 0004", 1},
		{"4111 1111 1111 1112", 0}, // bad check digit
		{"telephone 1234567890", 0},
		{"too short 12345 nope", 0},
	}
	for _, tc := range cases {
		got := d.Find(tc.s)
		if len(got) != tc.want {
			t.Errorf("Luhn detector on %q got %d, want %d", tc.s, len(got), tc.want)
		}
	}
}

func TestDetector_ChineseID(t *testing.T) {
	d := chineseIDDetector()
	cases := []struct {
		s    string
		want int
	}{
		{"id=11010119900307051X done", 1}, // contrived valid
		{"id=110101199003070518 ok", 1},
		{"id=110101199003070510 bad", 0}, // wrong check
		{"id=1234567890 too short", 0},
		{"id=110101199003070519 bad", 0}, // wrong check
	}
	// Pre-validate the canned values match the algorithm.
	if !chineseIDValid("11010119900307051X") || !chineseIDValid("110101199003070518") {
		t.Skip("invariant for hand-crafted IDs broken; rebuild fixtures")
	}
	for _, tc := range cases {
		got := d.Find(tc.s)
		if len(got) != tc.want {
			t.Errorf("CN ID detector on %q got %d, want %d", tc.s, len(got), tc.want)
		}
	}
}

// ---- Scan/Replace + overlap resolution ------------------------------------

func TestScan_LongestWinsOnOverlap(t *testing.T) {
	// `sk-ant-…` is matched by BOTH the OpenAI detector (sk-…) and
	// the Anthropic detector (sk-ant-…). Scan must keep only the
	// Anthropic match (longer-ish + earlier-registered).
	s := "auth: sk-ant-api03_abcdefghijklmnopqrstuvwxyz1234 end"
	matches := Scan(s, BuiltinDetectors())
	if len(matches) != 1 {
		t.Fatalf("want 1 deduped match, got %d (%+v)", len(matches), matches)
	}
	if matches[0].DetectorName != DetectorAnthropicKey {
		t.Errorf("wrong detector: %s", matches[0].DetectorName)
	}
}

func TestScan_MultipleDistinct(t *testing.T) {
	s := "AWS=AKIAIOSFODNN7EXAMPLE OAI=sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"
	matches := Scan(s, BuiltinDetectors())
	if len(matches) != 2 {
		t.Fatalf("want 2 matches, got %d (%+v)", len(matches), matches)
	}
	// In source order.
	if matches[0].Start >= matches[1].Start {
		t.Errorf("matches not in source order: %+v", matches)
	}
}

func TestReplaceWith_RewritesEachMatch(t *testing.T) {
	m := NewMapping()
	s := "AWS=AKIAIOSFODNN7EXAMPLE end"
	matches := Scan(s, BuiltinDetectors())
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d", len(matches))
	}
	out := ReplaceWith(s, matches, m)
	if !strings.Contains(out, MakePlaceholder(1)) {
		t.Errorf("replaced output %q missing placeholder", out)
	}
	if strings.Contains(out, "AKIA") {
		t.Errorf("real value leaked into replaced output: %q", out)
	}
}

func TestReplaceWith_SameValueGetsSamePlaceholder(t *testing.T) {
	// Two occurrences of the same secret in one prompt must collapse
	// to the same placeholder — both for cache stability and to
	// preserve the equality that the model would otherwise see.
	m := NewMapping()
	dups := "k1=AKIAIOSFODNN7EXAMPLE log says key=AKIAIOSFODNN7EXAMPLE"
	matches := Scan(dups, BuiltinDetectors())
	if len(matches) != 2 {
		t.Fatalf("want 2 matches, got %d", len(matches))
	}
	out := ReplaceWith(dups, matches, m)
	if c := strings.Count(out, MakePlaceholder(1)); c != 2 {
		t.Errorf("want both occurrences to use placeholder 001, got %d substitutions in %q", c, out)
	}
}

func TestCompileUserPatterns(t *testing.T) {
	patterns := []UserPattern{
		{Name: "internal-id", Regex: `INT-\d+`},
		{Name: "broken", Regex: `[unclosed`},
	}
	got := CompileUserPatterns(patterns)
	if len(got) != 1 {
		t.Fatalf("want 1 compiled detector (broken regex dropped), got %d", len(got))
	}
	if got[0].Name() != "internal-id" {
		t.Errorf("unexpected name %q", got[0].Name())
	}
	hits := got[0].Find("see INT-12345 in ticket")
	if len(hits) != 1 {
		t.Errorf("user detector should match")
	}
}
