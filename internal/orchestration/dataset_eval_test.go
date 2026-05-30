package orchestration_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/benjaminwestern/agentic-control/internal/orchestration"
	"github.com/benjaminwestern/agentic-control/pkg/contract"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
)

// mockEvalFanoutController intercepts fanout logic to return deterministic mock strings
// so we don't actually hit the network. We mock the `StartSession` and `SubscribeEvents` methods.
type mockEvalFanoutController struct {
	lastSessionID string
	mockText      string
	mockLogprobs  []contract.TokenLogprob
}

func (m *mockEvalFanoutController) Describe() contract.SystemDescriptor {
	return contract.SystemDescriptor{
		Runtimes: []contract.RuntimeDescriptor{
			{
				Runtime: "openaicompatible",
				Capabilities: contract.RuntimeCapabilities{
					StartSession: true,
					StreamEvents: true,
				},
				Probe: &contract.RuntimeProbe{
					Installed: true,
					Models: []contract.RuntimeModel{
						{ID: "gpt-4o-mini"},
						{ID: "gpt-4o"},
					},
				},
			},
		},
	}
}

func (m *mockEvalFanoutController) StartSession(ctx context.Context, runtime string, req api.StartSessionRequest) (*contract.RuntimeSession, error) {
	m.lastSessionID = req.SessionID
	return &contract.RuntimeSession{
		SessionID:         req.SessionID,
		ProviderSessionID: "mock-provider-sess",
		Runtime:           runtime,
		Model:             req.Model,
	}, nil
}

func (m *mockEvalFanoutController) SendInput(ctx context.Context, req api.SendInputRequest) (*contract.RuntimeEvent, error) {
	return nil, nil
}

func (m *mockEvalFanoutController) StopSession(ctx context.Context, sessionID string) (*contract.RuntimeEvent, error) {
	return nil, nil
}

func (m *mockEvalFanoutController) GetTrackedSession(ctx context.Context, sessionID string, providerID string) (*contract.TrackedSession, error) {
	return &contract.TrackedSession{
		Session: contract.RuntimeSession{
			SessionID: sessionID,
			Usage: contract.TokenUsage{
				TotalTokens: 50,
			},
			CostUSD: 0.01,
		},
	}, nil
}

func (m *mockEvalFanoutController) SubscribeEvents(buffer int) (<-chan contract.RuntimeEvent, func()) {
	ch := make(chan contract.RuntimeEvent, 10)
	go func() {
		time.Sleep(10 * time.Millisecond)
		ch <- contract.RuntimeEvent{
			SessionID: m.lastSessionID,
			EventType: "assistant.message.delta",
			Payload: map[string]any{
				"delta": m.mockText,
			},
		}
		payload := map[string]any{}
		if len(m.mockLogprobs) > 0 {
			payload["logprobs"] = m.mockLogprobs
		}
		ch <- contract.RuntimeEvent{
			SessionID: m.lastSessionID,
			EventType: contract.EventTurnCompleted,
			Payload:   payload,
		}
	}()
	return ch, func() {}
}

func TestRunBatchEvaluation(t *testing.T) {
	ctrl := &mockEvalFanoutController{
		mockText: `{"score": 4.5, "rationale": "Good answer", "passed": true}`,
	}

	items := []orchestration.DatasetItemRecord{
		{
			ID:           "item-1",
			InputPayload: "Hello",
			TargetOutput: "World",
		},
	}

	opts := orchestration.BatchEvaluationOptions{
		Items:       items,
		Prompt:      "rubric-accuracy",
		TargetModel: "openaicompatible=gpt-4o-mini",
		JudgeModel:  "openaicompatible=gpt-4o",
		Mode:        orchestration.ReductionModeEvaluate,
	}

	results, err := orchestration.RunBatchEvaluation(context.Background(), ctrl, opts)
	if err != nil {
		t.Fatalf("RunBatchEvaluation failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].Score != 4.5 {
		t.Errorf("Expected score 4.5, got %v", results[0].Score)
	}
	if !results[0].Passed {
		t.Errorf("Expected passed=true")
	}
	if results[0].CostUSD != 0.02 { // 0.01 target + 0.01 judge
		t.Errorf("Expected CostUSD 0.02, got %v", results[0].CostUSD)
	}
}

func TestRunBatchEvaluationGEvalUsesJudgeLogprobs(t *testing.T) {
	ctrl := &mockEvalFanoutController{
		mockText: "4",
		mockLogprobs: []contract.TokenLogprob{{
			Token:   "4",
			Logprob: -0.2,
			TopLogprobs: []contract.TokenLogprob{
				{Token: "4", Logprob: math.Log(0.25)},
				{Token: "5", Logprob: math.Log(0.75)},
			},
		}},
	}

	results, err := orchestration.RunBatchEvaluation(context.Background(), ctrl, orchestration.BatchEvaluationOptions{
		Items: []orchestration.DatasetItemRecord{{
			ID:           "item-1",
			InputPayload: "Hello",
			TargetOutput: "World",
		}},
		Prompt:      "rubric-accuracy",
		TargetModel: "openaicompatible=gpt-4o-mini",
		JudgeModel:  "openaicompatible=gpt-4o",
		Mode:        orchestration.ReductionModeGEval,
	})
	if err != nil {
		t.Fatalf("RunBatchEvaluation g-eval failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if math.Abs(results[0].Score-4.75) > 0.0001 {
		t.Fatalf("score = %v, want 4.75", results[0].Score)
	}
	if !results[0].Passed {
		t.Fatal("passed = false, want true")
	}
}

func TestRunJudgeAlignmentEvaluation(t *testing.T) {
	ctrl := &mockEvalFanoutController{
		mockText: `{"score": 4.0, "rationale": "Slightly different", "passed": true}`,
	}

	humanLabelled := `{"score": 5.0, "passed": true}`

	items := []orchestration.DatasetItemRecord{
		{
			ID:           "item-1",
			InputPayload: "Evaluate this text",
			TargetOutput: humanLabelled,
		},
	}

	opts := orchestration.JudgeAlignmentOptions{
		Items:      items,
		Prompt:     "rubric-accuracy",
		JudgeModel: "openaicompatible=gpt-4o",
		Mode:       orchestration.ReductionModeEvaluate,
	}

	metrics, err := orchestration.RunJudgeAlignmentEvaluation(context.Background(), ctrl, opts)
	if err != nil {
		t.Fatalf("RunJudgeAlignmentEvaluation failed: %v", err)
	}

	if metrics.TotalEvaluated != 1 {
		t.Fatalf("Expected 1 item evaluated, got %d", metrics.TotalEvaluated)
	}

	// diff = 4.0 - 5.0 = -1.0 -> squared = 1.0
	if metrics.MeanSquaredError != 1.0 {
		t.Errorf("Expected MSE 1.0, got %v", metrics.MeanSquaredError)
	}

	if metrics.Accuracy != 1.0 {
		t.Errorf("Expected Accuracy 1.0, got %v", metrics.Accuracy)
	}
}
