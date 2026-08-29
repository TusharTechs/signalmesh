package validator

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ReasonCode string

const (
	ReasonEmptyContent          ReasonCode = "EMPTY_CONTENT"
	ReasonResponseTooLong       ReasonCode = "RESPONSE_TOO_LONG"
	ReasonInvalidJSON           ReasonCode = "INVALID_JSON"
	ReasonMissingRequiredField  ReasonCode = "MISSING_REQUIRED_FIELD"
	ReasonEmptyAnswer           ReasonCode = "EMPTY_ANSWER"
	ReasonInvalidConfidenceType ReasonCode = "INVALID_CONFIDENCE_TYPE"
	ReasonConfidenceOutOfRange  ReasonCode = "CONFIDENCE_OUT_OF_RANGE"
	ReasonConfidenceTooLow      ReasonCode = "CONFIDENCE_TOO_LOW"
)

// Contract defines the semantic/response contract for a request.
// For Phase 2 we use a default contract. Later, contracts can be task-specific.
type Contract struct {
	RequiredFields  []string
	MinConfidence   float64
	MaxContentBytes int
}

// Result is the outcome of semantic validation.
type Result struct {
	Valid       bool
	Score       float64
	ReasonCodes []ReasonCode
	Details     string
}

// DefaultContract returns the default response contract used by the prototype.
func DefaultContract() Contract {
	return Contract{
		RequiredFields:  []string{"answer", "confidence"},
		MinConfidence:   0.70,
		MaxContentBytes: 32000,
	}
}

// Validate checks whether provider output satisfies the semantic contract.
// This is one of the key differentiators of SignalMesh:
// HTTP 200 does not necessarily mean the response is usable.
func Validate(content string, contract Contract) Result {
	var reasonCodes []ReasonCode
	var details []string

	add := func(code ReasonCode, detail string) {
		reasonCodes = append(reasonCodes, code)
		details = append(details, detail)
	}

	trimmed := strings.TrimSpace(content)

	if trimmed == "" {
		add(ReasonEmptyContent, "response content is empty")
		return Result{
			Valid:       false,
			Score:       0,
			ReasonCodes: reasonCodes,
			Details:     strings.Join(details, "; "),
		}
	}

	if len(trimmed) > contract.MaxContentBytes {
		add(
			ReasonResponseTooLong,
			fmt.Sprintf("response exceeds %d bytes", contract.MaxContentBytes),
		)
		return Result{
			Valid:       false,
			Score:       0,
			ReasonCodes: reasonCodes,
			Details:     strings.Join(details, "; "),
		}
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		add(ReasonInvalidJSON, "response is not valid JSON")
		return Result{
			Valid:       false,
			Score:       0,
			ReasonCodes: reasonCodes,
			Details:     strings.Join(details, "; "),
		}
	}

	for _, field := range contract.RequiredFields {
		if _, ok := payload[field]; !ok {
			add(
				ReasonMissingRequiredField,
				fmt.Sprintf("missing required field: %s", field),
			)
		}
	}

	if len(reasonCodes) > 0 {
		return Result{
			Valid:       false,
			Score:       0.1,
			ReasonCodes: reasonCodes,
			Details:     strings.Join(details, "; "),
		}
	}

	if answerVal, ok := payload["answer"]; ok {
		answerStr, ok := answerVal.(string)
		if !ok || strings.TrimSpace(answerStr) == "" {
			add(ReasonEmptyAnswer, "answer is empty or not a string")
		}
	}

	confidence := 0.0
	confidenceSeen := false

	if confVal, ok := payload["confidence"]; ok {
		switch v := confVal.(type) {
		case float64:
			confidence = v
			confidenceSeen = true
		case int:
			confidence = float64(v)
			confidenceSeen = true
		default:
			add(ReasonInvalidConfidenceType, "confidence must be a number")
		}

		if confidenceSeen {
			if confidence < 0 || confidence > 1 {
				add(ReasonConfidenceOutOfRange, "confidence must be between 0 and 1")
			} else if confidence < contract.MinConfidence {
				add(
					ReasonConfidenceTooLow,
					fmt.Sprintf(
						"confidence %.2f below required %.2f",
						confidence,
						contract.MinConfidence,
					),
				)
			}
		}
	}

	if len(reasonCodes) > 0 {
		score := 0.2
		if confidenceSeen && confidence >= 0 && confidence <= 1 {
			score = confidence * 0.5
		}

		return Result{
			Valid:       false,
			Score:       score,
			ReasonCodes: reasonCodes,
			Details:     strings.Join(details, "; "),
		}
	}

	score := confidence
	if score < 0 {
		score = 0.7
	}
	if score > 1 {
		score = 1
	}

	return Result{
		Valid:       true,
		Score:       score,
		ReasonCodes: nil,
		Details:     "response satisfies contract",
	}
}

// ReasonStrings converts typed reason codes into plain strings.
func (r Result) ReasonStrings() []string {
	out := make([]string, 0, len(r.ReasonCodes))
	for _, code := range r.ReasonCodes {
		out = append(out, string(code))
	}
	return out
}
