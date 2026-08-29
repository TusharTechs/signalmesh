package validator

import "testing"

func TestValidateValidResponse(t *testing.T) {
	content := `{"answer":"Paris","confidence":0.9}`
	result := Validate(content, DefaultContract())

	if !result.Valid {
		t.Fatalf("expected valid response, got: %+v", result)
	}

	if result.Score < 0.7 {
		t.Fatalf("expected score >= 0.7, got: %f", result.Score)
	}
}

func TestValidateMissingConfidence(t *testing.T) {
	content := `{"answer":"Paris"}`
	result := Validate(content, DefaultContract())

	if result.Valid {
		t.Fatalf("expected invalid response due to missing confidence, got: %+v", result)
	}
}

func TestValidateLowConfidence(t *testing.T) {
	content := `{"answer":"Paris","confidence":0.2}`
	result := Validate(content, DefaultContract())

	if result.Valid {
		t.Fatalf("expected invalid response due to low confidence, got: %+v", result)
	}
}

func TestValidateInvalidJSON(t *testing.T) {
	content := `{`
	result := Validate(content, DefaultContract())

	if result.Valid {
		t.Fatalf("expected invalid JSON response, got: %+v", result)
	}
}
