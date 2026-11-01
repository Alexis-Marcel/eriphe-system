package main

import (
	"context"
	"errors"
)

// The judge: an optional yes/no model a watch's condition may ask, for
// what a deterministic test cannot tell ("is this change about the
// price?"). It is shaped like a decision model's API (System One: a state,
// named closed questions, a probability per question), so that one plugs in
// without an adapter. Only the none judge exists: a network judge (Jev with
// the user's own key) or a local one (Ollama, reading the probability of
// "yes" in its logprobs) comes once a bench of real cases shows it catches
// what the rules miss.
//
// What a judge may do is bounded by code, whatever it answers: it only
// gates a wake that the deterministic condition already allowed. It never
// acts, never closes anything, never raises a notification: a judged wake
// is a tainted turn like any other from outside. A judge's score is a
// hint, never proof of trust.

// judgeQuestion is one closed question.
type judgeQuestion struct {
	Type string `json:"type"` // yesno
	Text string `json:"text"`
}

// Judge answers closed questions about a state with a probability each.
// The state holds external content in a delimited field: data, never an
// instruction to the judge.
type Judge interface {
	Ask(ctx context.Context, state map[string]any, questions map[string]judgeQuestion) (map[string]float64, error)
}

// errNoJudge: no judge is set up; the condition holds without it.
var errNoJudge = errors.New("no judge is set up")

// noJudge is the default: nothing leaves eriphe-core, nothing is judged.
type noJudge struct{}

func (noJudge) Ask(context.Context, map[string]any, map[string]judgeQuestion) (map[string]float64, error) {
	return nil, errNoJudge
}

// judgeThreshold is the probability of yes a judged wake needs.
const judgeThreshold = 0.5

// currentJudge is the judge in use: none, for now.
var currentJudge Judge = noJudge{}

// judgeAllows asks the judge a watch's question about what the condition
// saw. Without a judge, or when it fails, the deterministic condition
// alone decides (true): a judge may only hold back a wake, never make one.
func judgeAllows(ctx context.Context, question, excerpt string) (bool, string) {
	if question == "" {
		return true, ""
	}
	scores, err := currentJudge.Ask(ctx, map[string]any{"result_to_judge": excerpt},
		map[string]judgeQuestion{"q": {Type: "yesno", Text: question}})
	if err != nil {
		return true, "judge: " + err.Error()
	}
	p, ok := scores["q"]
	if !ok {
		return true, "judge: no answer"
	}
	return p >= judgeThreshold, ""
}
