package main

import (
	"regexp"
	"strings"
)

// One way to show an error, wherever it comes from (the brain, a task, a
// tool, an AI account): a sentence in the user's language that says the
// likely cause, what to do, and the raw text kept for "Details". The raw
// errors are a provider's, a harness's or a node's, in English: they are
// never the first thing shown. The sentences are in web_text_problems.go.

// problem is an error as the workspace shows it.
type problem struct {
	Cause  string // auth, quota, model, network, server, pending, offline, nonode or unknown
	Text   string // the sentence
	Action string // what to do, a link's label (Href)
	Href   string
	Detail string // the raw text, for "Details"
	Soft   bool   // not a failure: it is on its way
}

// Where an error happened, which words it gets.
const (
	whereBrain   = "brain"   // the assistant's own turn
	whereTask    = "task"    // a task given to an agent
	whereTool    = "tool"    // a tool of the catalog
	whereAccount = "account" // an AI account (provider)
)

// The causes an error is read as.
const (
	causeAuth    = "auth"
	causeQuota   = "quota"
	causeModel   = "model"
	causeNetwork = "network"
	causeServer  = "server"
	causePending = "pending"
	causeOffline = "offline"
	causeNoNode  = "nonode"
	causeUnknown = "unknown"
)

// accountCause tells whether a cause is the AI account's: its key, its
// quota, its models. A network that fails is not the account's.
func accountCause(cause string) bool {
	return cause == causeAuth || cause == causeQuota || cause == causeModel
}

// The known errors, the first that matches wins: what is only waiting
// first, and the model before the key, since the model's error names it.
var causeRules = []struct {
	cause string
	re    *regexp.Regexp
}{
	{causePending, regexp.MustCompile(`(?i)\bis (pending|starting) on\b|not placed yet`)},
	{causeNoNode, regexp.MustCompile(`(?i)has no node`)},
	{causeOffline, regexp.MustCompile(`(?i)node \S+ is not connected`)},
	{causeModel, regexp.MustCompile(`(?i)the agent runs model|model[^.]{0,80}(not[ _]found|does not exist|not available|unknown|invalid|not supported)|(unknown|invalid|no such) model|no endpoints found`)},
	{causeAuth, regexp.MustCompile(`(?i)unauthori[sz]ed|\b401\b|\b403\b|forbidden|failed to authenticate|authentication|invalid[ _-]?(api[ _-]?)?key|invalid x-api-key|please run /login|oauth token|token (has )?expired|invalid_grant|permission_error`)},
	{causeQuota, regexp.MustCompile(`(?i)\b429\b|\b402\b|rate[ _-]?limit|quota|credit balance|insufficient|billing|usage limit|limit reached|too many requests`)},
	{causeNetwork, regexp.MustCompile(`(?i)econnrefused|connection refused|enotfound|getaddrinfo|no such host|etimedout|timed? ?out|econnreset|connection reset|eai_again|unable to connect|fetch failed|network|firewall|proxy|dial tcp|unreachable`)},
	{causeServer, regexp.MustCompile(`(?i)overloaded|\b5\d\d\b|internal server error|server_error|bad gateway|service unavailable`)},
}

// classify reads a raw error as one of the causes.
func classify(raw string) string {
	for _, r := range causeRules {
		if r.re.MatchString(raw) {
			return r.cause
		}
	}
	return causeUnknown
}

// explain words a raw error that happened where.
func explain(lang, where, raw string) problem {
	return explainCause(lang, where, classify(raw), raw)
}

// explainCause words a cause already known (a test's), with its raw text.
func explainCause(lang, where, cause, raw string) problem {
	p := problem{Cause: cause, Detail: strings.TrimSpace(raw), Soft: cause == causePending}
	p.Text = problemText(lang, where, cause)
	if where == whereBrain || where == whereTask && accountCause(cause) {
		p.Action, p.Href = tr(lang, "problem.checkaccount"), accountsHref
	}
	return p
}

// accountsHref is where the AI accounts are.
const accountsHref = "/settings/agents#prov-title"

// problemText is a cause's sentence for where it happened, or its general one.
func problemText(lang, where, cause string) string {
	if k := "problem." + where + "." + cause; tr(lang, k) != k {
		return tr(lang, k)
	}
	return tr(lang, "problem."+cause)
}
