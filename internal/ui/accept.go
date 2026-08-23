package ui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/askexec"
)

type contractAcceptanceMsg struct {
	count int
	err   error
}

type contractAcceptance struct {
	ContractID   string   `json:"contract_id"`
	ResultSHA256 string   `json:"result_sha256"`
	Status       string   `json:"status"`
	Method       string   `json:"method"`
	Criteria     []string `json:"criteria"`
}

func (m *Model) commandAccept(args []string) (tea.Model, tea.Cmd) {
	if len(args) != 0 {
		m.notice = "usage: /accept"
		m.syncContent()
		return m, nil
	}
	if m.contractDraft != nil {
		m.composer.SetValue("")
		m.notice = "This is a contract draft · use /contract accept before work; /accept is only for reviewing results"
		m.syncContent()
		return m, nil
	}
	if m.pendingContract == nil {
		m.composer.SetValue("")
		m.notice = "Nothing is awaiting acceptance"
		m.syncContent()
		return m, nil
	}
	if len(m.pendingContract.OpenQuestions) > 0 {
		m.notice = "Cannot accept an unresolved decision · answer the contract question first"
		m.syncContent()
		return m, nil
	}
	if m.recorder == nil {
		m.notice = "Acceptance recorder is unavailable · review remains pending"
		m.syncContent()
		return m, nil
	}
	criteria := make([]string, 0, len(m.pendingContract.Outstanding))
	for _, criterion := range m.pendingContract.Outstanding {
		criteria = append(criteria, criterion.ID)
	}
	resultJSON, err := json.Marshal(m.pendingContract)
	if err != nil {
		m.notice = "Acceptance could not bind the pending result · review remains pending"
		m.syncContent()
		return m, nil
	}
	resultDigest := sha256.Sum256(resultJSON)
	record, err := json.Marshal(contractAcceptance{
		ContractID:   m.pendingContract.ContractID,
		ResultSHA256: fmt.Sprintf("sha256:%x", resultDigest),
		Status:       "accepted",
		Method:       "interactive-user",
		Criteria:     criteria,
	})
	if err != nil {
		m.notice = "Acceptance could not be encoded · review remains pending"
		m.syncContent()
		return m, nil
	}
	m.composer.SetValue("")
	m.composer.Blur()
	m.running = true
	m.activity = "recording user acceptance"
	m.notice = "Recording acceptance…"
	m.syncContent()
	recorder, session := m.recorder, m.session
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := recorder.Record(ctx, askexec.RecordRequest{
			Session: session, Source: "bench-user", Kind: "bench.contract-acceptance/v1", JSON: string(record),
		})
		return contractAcceptanceMsg{count: len(criteria), err: err}
	}
}

func (m *Model) updateContractAcceptance(msg contractAcceptanceMsg) (tea.Model, tea.Cmd) {
	m.running = false
	m.activity = ""
	if msg.err != nil {
		m.notice = "Acceptance was not recorded · review remains pending · " + msg.err.Error()
		m.syncContent()
		return m, m.composer.Focus()
	}
	if m.pendingContract == nil {
		m.notice = "Acceptance record arrived without a pending contract"
		m.syncContent()
		return m, m.composer.Focus()
	}
	m.pendingContract = nil
	m.taskOptions.Check = ""
	m.taskOptions.CheckAllCriteria = false
	m.taskOptions.Force = false
	m.messages = append(m.messages, message{role: roleOutcome, text: fmt.Sprintf("ACCEPTED BY YOU\nYou accepted %d remaining contract criterion/criteria after review.\nThe acceptance is sealed in the replayable session.", msg.count)})
	m.notice = fmt.Sprintf("Outcome accepted · you accepted %d contract criteria · check cleared · session is replayable", msg.count)
	m.syncContent()
	return m, m.composer.Focus()
}

func (m *Model) commandContinue(args []string) (tea.Model, tea.Cmd) {
	if len(args) != 0 {
		m.notice = "usage: /continue"
		m.syncContent()
		return m, nil
	}
	if m.pendingContract == nil && !m.retryContract {
		m.composer.SetValue("")
		m.notice = "Nothing is awaiting revision"
		m.syncContent()
		return m, nil
	}
	if m.retryContract {
		m.retryContract = false
		m.taskOptions.Force = true
		m.continueContract = m.admittedContract != nil
		m.composer.SetValue("")
		m.notice = "Loop retry armed · describe implementation guidance; the admitted contract and verifier stay unchanged"
		m.syncContent()
		return m, nil
	}
	if m.taskOptions.Check == "" {
		m.pendingContract = nil
		m.continueContract = m.admittedContract != nil
		m.notice = "Review released · describe implementation guidance to continue under the admitted contract"
	} else {
		m.pendingContract = nil
		m.taskOptions.Force = true
		m.continueContract = m.admittedContract != nil
		m.notice = "Continue armed · describe implementation guidance; the admitted contract stays unchanged"
	}
	m.composer.SetValue("")
	m.syncContent()
	return m, nil
}
