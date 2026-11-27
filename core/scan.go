package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// The scanner reads a text about to be kept (a node's page, a notification
// rule, a brain setting) for what must not be: instructions to the model,
// invisible characters, secrets and tokenised links are refused, softer
// imperative wording is flagged.

// scanResult is what the scanner found.
type scanResult struct {
	Reject string // why the text is refused
	Flags  []string
}

var (
	injectionRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override)\b.{0,30}\b(previous|prior|above|earlier|all|any|your|the)\b.{0,20}\b(instructions?|prompts?|rules|directives|guidelines|messages?)`),
		regexp.MustCompile(`(?i)\b(ignore|oublie|oubliez|ignorez|ne tiens pas compte)\b.{0,30}\b(les|tes|toutes|vos|des)\b.{0,25}\b(instructions?|consignes?|règles|regles|messages? précédents?)`),
		regexp.MustCompile(`(?i)\b(system|developer)\s*(prompt|message|instructions?)\b`),
		regexp.MustCompile(`(?i)\b(prompt|message|consignes?)\s+(système|systeme)\b`),
		regexp.MustCompile(`(?i)\byou are now\b|\btu es (maintenant|désormais|desormais)\b|\bnew instructions\b|\bnouvelles? (consignes?|instructions?)\b`),
		regexp.MustCompile(`(?i)<\s*/?\s*(system|assistant|user|instructions?|tool_call|function_calls?)\s*>|\[/?INST\]|<\|im_start\|>|<\|endoftext\|>`),
		regexp.MustCompile(`(?i)\b(jailbreak|DAN mode|developer mode)\b`),
	}
	// Softer: wording that tells the assistant what to do. Fine in my own
	// lessons, suspicious anywhere else.
	imperativeRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(always|never|whenever|from now on|you must|you should)\b`),
		regexp.MustCompile(`(?i)\b(toujours|jamais|dorénavant|dorenavant|désormais|desormais|à partir de maintenant|tu dois|il faut que tu|chaque fois que)\b`),
		regexp.MustCompile(`(?i)\b(call|run|execute|send|forward|transfer|exécute|execute|envoie|transfère|transfere|appelle)\b.{0,40}\b(tool|outil|run_task|answer_task|mail|e-mail|email|token|jeton|password|mot de passe)\b`),
	}
	secretRes = []*regexp.Regexp{
		regexp.MustCompile(`\b(sk|rk|pk)-(ant-|proj-|live-|test-)?[A-Za-z0-9_\-]{16,}`),
		regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{30,}`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`),
		regexp.MustCompile(`(?i)\b(password|passwd|mot de passe|mdp|api[_ -]?key|secret|token|jeton)\s*[:=]\s*\S{6,}`),
	}
	longTokenRe = regexp.MustCompile(`[A-Za-z0-9+/_\-]{40,}`)
	urlRe2      = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s<>"'\x60]+`)
	secretKeys  = regexp.MustCompile(`(?i)^(token|access_token|refresh_token|id_token|auth|key|api_key|apikey|secret|sig|signature|password|pass|pwd|code|session|sessionid|jwt|x-amz-signature|x-amz-credential)$`)
)

// invisible tells the characters that hide text from me: zero-width ones,
// bidirectional controls, tags, and other format characters.
func invisible(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064,
		r >= 0x2066 && r <= 0x2069, r == 0xFEFF, r == 0x00AD, r == 0x180E, r >= 0xE0000 && r <= 0xE007F:
		return true
	case r == '\n' || r == '\t' || r == '\r':
		return false
	}
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r) || (unicode.IsControl(r))
}

// scanText checks what is about to be written.
func scanText(parts ...string) scanResult {
	var res scanResult
	text := strings.Join(parts, "\n")
	for _, r := range text {
		if invisible(r) {
			res.Reject = fmt.Sprintf("invisible or control character U+%04X", r)
			return res
		}
	}
	for _, re := range injectionRes {
		if re.MatchString(text) {
			res.Reject = "it reads like instructions to an assistant, not like something to remember"
			return res
		}
	}
	if why := scanSecrets(text); why != "" {
		res.Reject = why
		return res
	}
	for _, raw := range urlRe2.FindAllString(text, -1) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)"))
		if err != nil {
			res.Flags = append(res.Flags, "url")
			continue
		}
		if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "obsidian" {
			res.Flags = append(res.Flags, "url-scheme")
		}
	}
	for _, re := range imperativeRes {
		if re.MatchString(text) {
			res.Flags = append(res.Flags, "imperative")
			break
		}
	}
	return res
}

// scanSecrets tells why a text holds a secret: a credential, a long
// random string, a link with a password or a token; "" when it holds
// none. The intentions and the graph's nodes (nodes.go) refuse such a text.
func scanSecrets(text string) string { return scanSecretsNamed(text, text) }

// scanSecretsNamed is scanSecrets with the long random strings looked for
// in named: text with what names an item left out (a node's item
// references, refuseSecret). A credential's known shape and a link's
// password or token are still looked for in the whole text.
func scanSecretsNamed(text, named string) string {
	for _, re := range secretRes {
		if re.MatchString(text) {
			return "it looks like a credential: secrets belong in the vault"
		}
	}
	for _, m := range longTokenRe.FindAllString(withoutLinkPaths(named), -1) {
		if looksRandom(m) {
			return "it holds a long random string, which looks like a credential"
		}
	}
	for _, raw := range urlRe2.FindAllString(text, -1) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)"))
		if err != nil {
			continue
		}
		if u.User != nil {
			return "a link carries a user name or password"
		}
		frag, _ := url.ParseQuery(u.Fragment) // an access token comes back in a fragment too
		for _, q := range []url.Values{u.Query(), frag} {
			for k := range q {
				if secretKeys.MatchString(k) {
					return "a link carries a token (" + k + "=…): keep the link without it"
				}
			}
		}
	}
	return ""
}

// withoutLinkPaths is text with each link's address and path left out,
// its query and fragment kept: a path names things (a repository, a pull
// request, a document's id), and its segments joined by / or a document's
// id would read as a long random string. What a link carries as a token
// is still seen: a credential's known shape anywhere (secretRes), a
// query's or fragment's key or long random value, a user name or password.
func withoutLinkPaths(text string) string {
	return urlRe2.ReplaceAllStringFunc(text, func(raw string) string {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)"))
		if err != nil || u.Opaque != "" {
			return raw
		}
		return " " + u.Host + " " + u.RawQuery + " " + u.Fragment + " "
	})
}

// looksRandom tells a credential-like string from a word or a path: digits,
// lower and upper case letters all present.
func looksRandom(s string) bool {
	var lower, upper, digit bool
	for _, r := range s {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return lower && upper && digit
}
