package autoroute

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/patrickyoung/bench/internal/askexec"
	"github.com/patrickyoung/bench/internal/autonomy"
	"github.com/patrickyoung/bench/internal/plyexec"
)

func TestParseSeparatesSuggestionFromExecutionAuthority(t *testing.T) {
	base := Request{Task: plyexec.TaskRequest{Toolbox: "/tools/workspace", Options: plyexec.TaskOptions{IntentContract: true}}}
	body := []byte(`{"version":1,"route":"quick","reason":"routine-local","risk_tags":[]}`)
	decision, err := Parse(body, base)
	if err != nil || decision.Suggested != autonomy.Quick || decision.Effective != autonomy.Review || decision.Clamped != "quick-not-authorized" {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	base.AllowQuick = true
	decision, err = Parse(body, base)
	if err != nil || decision.Effective != autonomy.Quick || decision.Clamped != "" {
		t.Fatalf("authorized decision=%#v err=%v", decision, err)
	}
}

func TestParseRejectsMissingNullAndOversizedRequiredFields(t *testing.T) {
	req := Request{AllowQuick: true, Task: plyexec.TaskRequest{Toolbox: "/tools"}}
	for _, body := range []string{
		`{"version":1,"reason":"routine-local","risk_tags":[]}`,
		`{"version":1,"route":null,"reason":"routine-local","risk_tags":[]}`,
		`{"version":1,"route":"quick","reason":"routine-local"}`,
		`{"version":1,"route":"quick","reason":"routine-local","risk_tags":null}`,
		`{"version":1,"route":"review","reason":null,"risk_tags":[]}`,
		`{"version":null,"route":"review","reason":"open-decision","risk_tags":[]}`,
		`{"version":1,"route":"review","reason":"consequential-effect","risk_tags":["account_change","cost","credential","data_egress","destructive","external_communication","financial","network","outside_workspace","physical_action","production","publish","system_change"]}`,
	} {
		if got, err := Parse([]byte(body), req); err == nil {
			t.Fatalf("accepted %s as %#v", body, got)
		}
	}
}

func TestPolicyCanOnlyEscalateModelSuggestion(t *testing.T) {
	cases := []struct {
		name string
		body string
		req  Request
		want string
	}{
		{"risk", `{"version":1,"route":"quick","reason":"routine-local","risk_tags":["publish"]}`, Request{AllowQuick: true, Task: plyexec.TaskRequest{Toolbox: "/tools"}}, "consequential-effect"},
		{"full shell", `{"version":1,"route":"quick","reason":"routine-local","risk_tags":[]}`, Request{AllowQuick: true, Task: plyexec.TaskRequest{}}, "broad-authority"},
		{"loop without check", `{"version":1,"route":"loop","reason":"checked-pursuit","risk_tags":[]}`, Request{Task: plyexec.TaskRequest{Toolbox: "/tools"}}, "loop-policy"},
		{"loop without finite explicit bound", `{"version":1,"route":"loop","reason":"checked-pursuit","risk_tags":[]}`, Request{Task: plyexec.TaskRequest{Toolbox: "/tools", Options: plyexec.TaskOptions{Check: "true", HasTurns: true, Turns: 0}}}, "loop-policy"},
		{"loop with broad shell", `{"version":1,"route":"loop","reason":"checked-pursuit","risk_tags":[]}`, Request{Task: plyexec.TaskRequest{Options: plyexec.TaskOptions{Check: "true"}}}, "broad-authority"},
		{"loop with consequential tag", `{"version":1,"route":"loop","reason":"checked-pursuit","risk_tags":["publish"]}`, Request{Task: plyexec.TaskRequest{Toolbox: "/tools", Options: plyexec.TaskOptions{Check: "true"}}}, "consequential-effect"},
		{"admitted boundary", `{"version":1,"route":"quick","reason":"routine-local","risk_tags":[]}`, Request{AllowQuick: true, Task: plyexec.TaskRequest{Toolbox: "/tools", Options: plyexec.TaskOptions{ApprovalPolicy: plyexec.ApprovalEveryAction}}}, "admitted-boundary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.body), tc.req)
			if err != nil || got.Effective != autonomy.Review || got.Clamped != tc.want {
				t.Fatalf("decision=%#v err=%v", got, err)
			}
		})
	}
}

func TestRouterUsesOneStructuredAskTurnAndHidesClassifierJSON(t *testing.T) {
	ask := &fakeAsk{events: []askexec.Event{
		{Stream: askexec.Stderr, Text: "thinking\n"},
		{Stream: askexec.Stdout, Text: `{"version":1,"route":"review","reason":"open-decision","risk_tags":[]}`},
		{Done: true, ExitCode: 0},
	}}
	events := (Runner{Ask: ask, Recorder: ask}).Route(context.Background(), Request{Task: plyexec.TaskRequest{Goal: "decide what to publish", Input: "draft evidence\n", Session: "/sessions/run.jsonl", Toolbox: "/tools/workspace"}})
	var text string
	var terminal Event
	for event := range events {
		text += event.Text
		if event.Done {
			terminal = event
		}
	}
	if terminal.Decision == nil || terminal.Decision.Effective != autonomy.Review || text != "thinking\n" || strings.Contains(text, "version") {
		t.Fatalf("text=%q terminal=%#v", text, terminal)
	}
	if ask.request.Schema != Schema || ask.request.System != System || ask.request.Input != "draft evidence\n" || !strings.Contains(ask.request.Message, "PIPED EVIDENCE") {
		t.Fatalf("request=%#v", ask.request)
	}
	if ask.record.Kind != "bench.route/v1" || !strings.Contains(ask.record.JSON, `"authority":"explicit-mode-auto"`) || !strings.Contains(ask.record.JSON, `"selected":"review"`) || !strings.Contains(ask.record.JSON, `"input_present":true`) || !strings.Contains(ask.record.JSON, `"tool_grant":"toolbox"`) {
		t.Fatalf("record=%#v", ask.record)
	}
}

func TestControllerFloorsOverrideAskAndStillSealDecision(t *testing.T) {
	ask := &fakeAsk{events: []askexec.Event{{Stream: askexec.Stdout, Text: `{"version":1,"route":"quick","reason":"routine-local","risk_tags":[]}`}, {Done: true, ExitCode: 0}}}
	events := (Runner{Ask: ask, Recorder: ask}).Route(context.Background(), Request{AllowQuick: true, Task: plyexec.TaskRequest{Goal: "change it", Session: "/sessions/run.jsonl"}})
	var terminal Event
	for event := range events {
		if event.Done {
			terminal = event
		}
	}
	if terminal.Decision == nil || terminal.Decision.Effective != autonomy.Review || terminal.Decision.Clamped != "broad-authority" || ask.request.Message == "" || ask.record.Kind != "bench.route/v1" {
		t.Fatalf("terminal=%#v ask=%#v", terminal, ask)
	}
}

func TestRouterFailureOrInvalidOutputFallsBackToRecordedReview(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []askexec.Event
		clamp  string
	}{
		{"failed", []askexec.Event{{Done: true, ExitCode: 1, Err: errors.New("provider unavailable")}}, "router-failed"},
		{"invalid", []askexec.Event{{Stream: askexec.Stdout, Text: `{"version":1,"route":"quick"}`}, {Done: true, ExitCode: 0}}, "router-invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ask := &fakeAsk{events: tc.events}
			var terminal Event
			for event := range (Runner{Ask: ask, Recorder: ask}).Route(context.Background(), Request{AllowQuick: true, Task: plyexec.TaskRequest{Goal: "change it", Session: "/sessions/existing.jsonl", Toolbox: "/tools"}}) {
				if event.Done {
					terminal = event
				}
			}
			if terminal.ExitCode != 0 || terminal.Decision == nil || terminal.Decision.Effective != autonomy.Review || terminal.Decision.Clamped != tc.clamp || ask.record.Kind != "bench.route/v1" {
				t.Fatalf("terminal=%#v record=%#v", terminal, ask.record)
			}
		})
	}
}

func TestRouteRecordFailureStartsNoDownstreamAuthority(t *testing.T) {
	ask := &fakeAsk{recordErr: errors.New("seal failed")}
	events := (Runner{Ask: ask, Recorder: ask}).Route(context.Background(), Request{AllowQuick: true, Task: plyexec.TaskRequest{Goal: "change it", Session: "/sessions/run.jsonl"}})
	var terminal Event
	for event := range events {
		if event.Done {
			terminal = event
		}
	}
	if terminal.ExitCode != 1 || terminal.Err == nil || terminal.Decision != nil {
		t.Fatalf("terminal=%#v", terminal)
	}
}

func TestCancellationDoesNotRecordFallbackOrDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ask := &fakeAsk{events: []askexec.Event{{Done: true, ExitCode: 130, Err: context.Canceled}}}
	var terminal Event
	for event := range (Runner{Ask: ask, Recorder: ask}).Route(ctx, Request{AllowQuick: true, Task: plyexec.TaskRequest{Goal: "change it", Session: "/sessions/run.jsonl", Toolbox: "/tools"}}) {
		if event.Done {
			terminal = event
		}
	}
	if terminal.ExitCode != 130 || !errors.Is(terminal.Err, context.Canceled) || ask.record.Kind != "" {
		t.Fatalf("terminal=%#v record=%#v", terminal, ask.record)
	}
}

func TestCancellationAtRecordBoundaryReturnsNoDecision(t *testing.T) {
	for _, recordErr := range []error{nil, context.Canceled} {
		ctx, cancel := context.WithCancel(context.Background())
		ask := &fakeAsk{
			events:     []askexec.Event{{Stream: askexec.Stdout, Text: `{"version":1,"route":"quick","reason":"routine-local","risk_tags":[]}`}, {Done: true, ExitCode: 0}},
			recordHook: cancel, recordErr: recordErr,
		}
		var terminal Event
		for event := range (Runner{Ask: ask, Recorder: ask}).Route(ctx, Request{AllowQuick: true, Task: plyexec.TaskRequest{Goal: "change it", Session: "/sessions/run.jsonl", Toolbox: "/tools"}}) {
			if event.Done {
				terminal = event
			}
		}
		if terminal.ExitCode != 130 || terminal.Decision != nil || !errors.Is(terminal.Err, context.Canceled) {
			t.Fatalf("recordErr=%v terminal=%#v", recordErr, terminal)
		}
	}
}

type fakeAsk struct {
	events     []askexec.Event
	request    askexec.Request
	record     askexec.RecordRequest
	recordErr  error
	recordHook func()
}

func (f *fakeAsk) Record(_ context.Context, req askexec.RecordRequest) error {
	f.record = req
	if f.recordHook != nil {
		f.recordHook()
	}
	return f.recordErr
}

func (f *fakeAsk) Start(_ context.Context, req askexec.Request) <-chan askexec.Event {
	f.request = req
	out := make(chan askexec.Event, len(f.events))
	for _, event := range f.events {
		out <- event
	}
	close(out)
	return out
}
