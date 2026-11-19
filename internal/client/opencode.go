package client

import "dbohdan.com/strument/internal/llm"

// ocBilling decodes usage.opencode, which opencode sends in the usage of all
// three dialects: the chat-completions final chunk, the Messages
// message_delta, and the Responses response.completed. Its numbers may come
// quoted, like opencode's cost.
type ocBilling struct {
	BillingSource string    `json:"billing_source"`
	ListCost      flexFloat `json:"list_cost"`
	Allowance     flexFloat `json:"allowance"`
}

func (o *ocBilling) toLLM() *llm.OpencodeBilling {
	if o == nil || (o.BillingSource == "" && !o.Allowance.Known) {
		return nil
	}
	return &llm.OpencodeBilling{Source: o.BillingSource, ListCost: o.ListCost.Value, Allowance: o.Allowance.Value}
}
