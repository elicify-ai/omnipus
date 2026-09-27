package gateway

// rest_mail_budget.go — the A8 mail-operation budget at the REST surface:
// the four dialing Mail panel GETs (folders / list / message / attachment)
// run their IMAP dial through the shared per-account budget
// (email.MailBudget::CallValue), so an automatic panel poll during a watcher
// backoff window returns the contract's 503 MailUnavailableError without
// dialing, the human Retry click (?retry=true) bypasses ONLY the backoff
// check (the semaphore and coalescing still apply — proven at the budget
// unit), and the 2-per-account cap is shared with the agent tools and the
// watcher cycles.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// mailBudgetFor resolves the restAPI's handle on the shared A8 budget. Boot
// injects the process-wide instance at restAPI construction
// (gateway_boot.go); the lazy fallback keeps a directly constructed restAPI
// (the unit-test literal) on the SAME process-wide instance —
// email.SharedMailBudget is keyed by the state dir, so the gate is shared no
// matter how the restAPI was built.
func (a *restAPI) mailBudgetFor() *email.MailBudget {
	a.mailBudgetOnce.Do(func() {
		if a.mailBudget == nil {
			a.mailBudget = email.SharedMailBudget(a.homePath)
		}
	})
	return a.mailBudget
}

// mailRetryParam parses the ?retry= query parameter — the human Retry click.
// An unparsable value counts as false: the automatic (gated) shape.
func mailRetryParam(r *http.Request) bool {
	b, err := strconv.ParseBool(r.URL.Query().Get("retry"))
	return err == nil && b
}

// mailBudgetErr maps a budget/transport error to the HTTP response. A typed
// backoff refusal and ErrMailBusy become the contract's 503
// MailUnavailableError (code backoff / busy); every other error is the MC-8
// 502 envelope — an upstream failure AFTER a dial happened. Returns false
// when err is none of these (caller continues).
func (a *restAPI) mailBudgetErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var bf *email.MailBackoffError
	if errors.As(err, &bf) {
		body := gen.MailUnavailableError{
			Code:  gen.MailUnavailableErrorCodeBackoff,
			Error: "mailbox in backoff — the watcher will retry automatically",
		}
		if bf.LastErrorClass != "" {
			cls := gen.MailUnavailableErrorLastErrorClass(bf.LastErrorClass)
			body.LastErrorClass = &cls
		}
		if next, perr := time.Parse(time.RFC3339, bf.NextAttemptAt); perr == nil {
			body.NextAttemptAt = &next
		}
		writeJSON(w, http.StatusServiceUnavailable, body)
		return true
	}
	if errors.Is(err, email.ErrMailBusy) {
		writeJSON(w, http.StatusServiceUnavailable, gen.MailUnavailableError{
			Code:  gen.MailUnavailableErrorCodeBusy,
			Error: "mailbox busy — the request queued past its deadline under the 2-per-account cap",
		})
		return true
	}
	return false
}

// mailBudgetWrap gates one panel dial through the shared budget and maps the
// budget refusals to the contract's 503. Returns true when the response was
// fully handled (refusal or dial error); false when the dial succeeded and
// the caller should render v.
func (a *restAPI) mailBudgetWrap(
	w http.ResponseWriter,
	r *http.Request,
	agentID, workspaceID string,
	client *email.Client,
	op string,
	params map[string]any,
	dial func(context.Context) (any, error),
) (any, bool) {
	budget := a.mailBudgetFor()
	req := email.MailBudgetRequest{
		Account:     client.AccountKey(),
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		Operation:   op,
		Params:      params,
		Retry:       mailRetryParam(r),
	}
	v, err := budget.CallValue(r.Context(), req, dial)
	if a.mailBudgetErr(w, err) {
		return nil, true
	}
	if err != nil {
		// Not a budget refusal: an upstream dial failure (MC-8 502) or an
		// invalid ref (400).
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return nil, true
	}
	return v, false
}
