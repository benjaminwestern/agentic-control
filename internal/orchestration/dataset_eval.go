package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/benjaminwestern/agentic-control/internal/idgen"
	sigmaevals "github.com/benjaminwestern/sigma-evals"
)

type BatchEvaluationOptions struct {
	Items       []DatasetItemRecord
	Prompt      string
	TargetModel string
	JudgeModel  string
	Mode        ReductionMode
}

func parseEvalTarget(raw string) FanoutTarget {
	parts := strings.SplitN(raw, "=", 2)
	if len(parts) == 1 {
		return FanoutTarget{Backend: "openaicompatible", Model: raw}
	}
	return FanoutTarget{Backend: parts[0], Model: parts[1]}
}

func RunBatchEvaluation(ctx context.Context, controller FanoutController, opts BatchEvaluationOptions) ([]EvaluationResultRecord, error) {
	evaluator := sigmaevals.NewTargetEvaluator(controlPlaneSigmaCompleter{controller: controller})
	target := sigmaEvalTargetFromEvalTarget(opts.TargetModel)
	judge := sigmaEvalTargetFromEvalTarget(opts.JudgeModel)
	mode := sigmaEvalMode(opts.Mode)

	var results []EvaluationResultRecord
	for _, item := range opts.Items {
		start := time.Now()
		judgeResult, err := evaluator.Evaluate(ctx, sigmaevals.EvaluateInput{
			Input:        item.InputPayload,
			GroundTruth:  item.TargetOutput,
			Rubric:       rubricPrompt(opts.Prompt),
			TargetPrompt: opts.Prompt,
			Target:       target,
			Judge:        judge,
			Mode:         mode,
		})
		if err != nil {
			return results, fmt.Errorf("evaluate item %s: %w", item.ID, err)
		}

		results = append(results, EvaluationResultRecord{
			ID:            idgen.NewUUID(),
			DatasetItemID: item.ID,
			Score:         judgeResult.Score,
			Rationale:     judgeResult.Rationale,
			Passed:        judgeResult.Passed,
			LatencyMS:     int(time.Since(start).Milliseconds()),
			CostUSD:       sigmaMessageCostUSD(judgeResult.TargetMessage, judgeResult.JudgeMessage),
		})
	}

	return results, nil
}

type JudgeAlignmentOptions struct {
	Items      []DatasetItemRecord
	Prompt     string
	JudgeModel string
	Mode       ReductionMode
}

type JudgeAlignmentMetrics struct {
	MeanSquaredError float64 `json:"mean_squared_error"`
	Accuracy         float64 `json:"accuracy"`
	TotalEvaluated   int     `json:"total_evaluated"`
	CostUSD          float64 `json:"cost_usd"`
}

func RunJudgeAlignmentEvaluation(ctx context.Context, controller FanoutController, opts JudgeAlignmentOptions) (JudgeAlignmentMetrics, error) {
	var metrics JudgeAlignmentMetrics
	cases := make([]sigmaevals.JudgeAlignmentCase, 0, len(opts.Items))
	for _, item := range opts.Items {
		var humanScore struct {
			Score  float64 `json:"score"`
			Passed bool    `json:"passed"`
		}
		if err := json.Unmarshal([]byte(item.TargetOutput), &humanScore); err != nil {
			continue
		}
		cases = append(cases, sigmaevals.JudgeAlignmentCase{
			ID:             item.ID,
			TargetOutput:   item.InputPayload,
			Rubric:         rubricPrompt(opts.Prompt),
			ExpectedScore:  humanScore.Score,
			ExpectedPassed: humanScore.Passed,
		})
	}
	if len(cases) == 0 {
		return metrics, nil
	}

	result, err := sigmaevals.NewTargetEvaluator(controlPlaneSigmaCompleter{controller: controller}).EvaluateJudges(ctx, sigmaevals.JudgeAlignmentSpec{
		Name:         "judge-alignment",
		Cases:        cases,
		JudgeTargets: []sigmaevals.Target{sigmaEvalTargetFromEvalTarget(opts.JudgeModel)},
		Mode:         sigmaEvalMode(opts.Mode),
	})
	if err != nil {
		return metrics, err
	}

	metrics.TotalEvaluated = result.Summary.Regression.Count
	metrics.MeanSquaredError = result.Summary.Regression.MeanSquaredError
	metrics.Accuracy = result.Summary.Classification.Accuracy
	for _, item := range result.Results {
		if item.Result != nil {
			metrics.CostUSD += sigmaMessageCostUSD(item.Result.JudgeMessage)
		}
	}
	return metrics, nil
}

type DatasetEvaluationOptions struct {
	DatasetID   string
	PromptID    string
	TargetModel string
	JudgeModel  string
	Name        string
	Mode        ReductionMode
}

func RunDatasetEvaluation(ctx context.Context, ledger *SQLiteLedgerStore, controller FanoutController, opts DatasetEvaluationOptions) (EvaluationRecord, error) {
	_, err := ledger.GetDataset(ctx, opts.DatasetID)
	if err != nil {
		return EvaluationRecord{}, fmt.Errorf("get dataset: %w", err)
	}
	prompt, err := ledger.GetPrompt(ctx, opts.PromptID)
	if err != nil {
		return EvaluationRecord{}, fmt.Errorf("get prompt: %w", err)
	}

	eval := EvaluationRecord{
		ID:          idgen.NewUUID(),
		Name:        opts.Name,
		DatasetID:   opts.DatasetID,
		PromptID:    opts.PromptID,
		TargetModel: opts.TargetModel,
		JudgeModel:  opts.JudgeModel,
	}
	if err := ledger.UpsertEvaluation(ctx, eval); err != nil {
		return EvaluationRecord{}, fmt.Errorf("create evaluation record: %w", err)
	}

	items, err := ledger.ListDatasetItems(ctx, opts.DatasetID)
	if err != nil {
		return EvaluationRecord{}, fmt.Errorf("list dataset items: %w", err)
	}

	results, err := RunBatchEvaluation(ctx, controller, BatchEvaluationOptions{
		Items:       items,
		Prompt:      prompt.Content,
		TargetModel: opts.TargetModel,
		JudgeModel:  opts.JudgeModel,
		Mode:        opts.Mode,
	})
	if err != nil {
		return EvaluationRecord{}, err
	}

	for _, res := range results {
		res.EvaluationID = eval.ID
		if err := ledger.AddEvaluationResult(ctx, res); err != nil {
			return eval, fmt.Errorf("add evaluation result for item %s: %w", res.DatasetItemID, err)
		}
	}

	return eval, nil
}
