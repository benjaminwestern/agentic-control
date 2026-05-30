package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/benjaminwestern/agentic-control/internal/orchestration"
	api "github.com/benjaminwestern/agentic-control/pkg/controlplane"
	"github.com/spf13/cobra"
)

func newDatasetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dataset",
		Short: "Manage synthetic datasets and evaluations",
	}

	cmd.AddCommand(newDatasetSynthCmd())
	cmd.AddCommand(newDatasetGenerateSchemaCmd())
	cmd.AddCommand(newDatasetEvalCmd())

	return cmd
}

func newDatasetEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run evaluations against datasets",
	}

	var socketPath string
	var itemsFile string
	var prompt string
	var targetModel string
	var judgeModel string
	var name string
	var mode string

	judgeCmd := &cobra.Command{
		Use:   "judge",
		Short: "Evaluate the judge model against a golden dataset (MSE/Alignment)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			b, err := os.ReadFile(itemsFile)
			if err != nil {
				return fmt.Errorf("read items file: %w", err)
			}
			var items []orchestration.DatasetItemRecord
			if err := json.Unmarshal(b, &items); err != nil {
				return fmt.Errorf("parse items file: %w", err)
			}

			controller := fanoutController(socketPath)

			res, err := orchestration.RunJudgeAlignmentEvaluation(ctx, controller, orchestration.JudgeAlignmentOptions{
				Items:      items,
				Prompt:     prompt,
				JudgeModel: judgeModel,
				Mode:       orchestration.ReductionMode(mode),
			})
			if err != nil {
				return err
			}

			out, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}

	judgeCmd.Flags().StringVar(&itemsFile, "items", "", "Path to JSON array of dataset items (TargetOutput must be JSON containing {score, passed})")
	judgeCmd.Flags().StringVar(&prompt, "prompt", "rubric-accuracy", "The rubric ID or full prompt to send to the judge model")
	judgeCmd.Flags().StringVar(&judgeModel, "judge-model", "openaicompatible=gpt-4o", "Judge model")
	judgeCmd.Flags().StringVar(&socketPath, "socket-path", "", "Optional daemon socket path")
	judgeCmd.Flags().StringVar(&mode, "mode", "evaluate", "Evaluation mode: 'evaluate' or 'g_eval'")

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run an evaluation on an items file using a target model and a judge model",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			b, err := os.ReadFile(itemsFile)
			if err != nil {
				return fmt.Errorf("read items file: %w", err)
			}
			var items []orchestration.DatasetItemRecord
			if err := json.Unmarshal(b, &items); err != nil {
				return fmt.Errorf("parse items file: %w", err)
			}

			controller := fanoutController(socketPath)

			res, err := orchestration.RunBatchEvaluation(ctx, controller, orchestration.BatchEvaluationOptions{
				Items:       items,
				Prompt:      prompt,
				TargetModel: targetModel,
				JudgeModel:  judgeModel,
				Mode:        orchestration.ReductionMode(mode),
			})
			if err != nil {
				return err
			}

			out, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}

	runCmd.Flags().StringVar(&itemsFile, "items", "", "Path to JSON array of dataset items")
	runCmd.Flags().StringVar(&prompt, "prompt", "rubric-accuracy", "The rubric ID or full prompt to send to the judge model")
	runCmd.Flags().StringVar(&targetModel, "target-model", "openaicompatible=gpt-4o-mini", "Target model")
	runCmd.Flags().StringVar(&judgeModel, "judge-model", "openaicompatible=gpt-4o", "Judge model")
	runCmd.Flags().StringVar(&name, "name", "CLI Evaluation", "Name of the evaluation")
	runCmd.Flags().StringVar(&socketPath, "socket-path", "", "Optional daemon socket path")
	runCmd.Flags().StringVar(&mode, "mode", "evaluate", "Evaluation mode: 'evaluate' or 'g_eval'")

	var inputPayload string
	var groundTruth string

	inlineCmd := &cobra.Command{
		Use:   "inline [input_payload]",
		Short: "Run an inline evaluation for a single input to test for drift",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if len(args) > 0 {
				inputPayload = args[0]
			}

			if inputPayload == "" {
				return fmt.Errorf("input payload is required either via arg or flag")
			}

			controller := fanoutController(socketPath)

			items := []orchestration.DatasetItemRecord{
				{
					ID:           "inline-1",
					InputPayload: inputPayload,
					TargetOutput: groundTruth,
				},
			}

			res, err := orchestration.RunBatchEvaluation(ctx, controller, orchestration.BatchEvaluationOptions{
				Items:       items,
				Prompt:      prompt,
				TargetModel: targetModel,
				JudgeModel:  judgeModel,
				Mode:        orchestration.ReductionMode(mode),
			})
			if err != nil {
				return err
			}

			if len(res) == 0 {
				return fmt.Errorf("evaluation produced no results")
			}

			out, _ := json.MarshalIndent(res[0], "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}

	inlineCmd.Flags().StringVar(&inputPayload, "input", "", "The input text (or pass as first positional argument)")
	inlineCmd.Flags().StringVar(&groundTruth, "ground-truth", "", "Optional expected ground truth for comparison")
	inlineCmd.Flags().StringVar(&prompt, "prompt", "rubric-persona-drift", "The rubric ID or full prompt to send to the judge model (default: rubric-persona-drift)")
	inlineCmd.Flags().StringVar(&targetModel, "target-model", "openaicompatible=gpt-4o-mini", "Target model")
	inlineCmd.Flags().StringVar(&judgeModel, "judge-model", "openaicompatible=gpt-4o", "Judge model")
	inlineCmd.Flags().StringVar(&socketPath, "socket-path", "", "Optional daemon socket path")
	inlineCmd.Flags().StringVar(&mode, "mode", "evaluate", "Evaluation mode: 'evaluate' or 'g_eval'")

	cmd.AddCommand(judgeCmd)
	cmd.AddCommand(runCmd)
	cmd.AddCommand(inlineCmd)
	return cmd
}

func newDatasetSynthCmd() *cobra.Command {
	var count int
	var schemaFile string
	var outFormat string
	var socketPath string
	var targets []string
	var reasoningEffort string

	cmd := &cobra.Command{
		Use:   "synth [base_prompt]",
		Short: "Generate synthetic data using zero-shot with schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			prompt := args[0]

			var schema map[string]any
			if schemaFile != "" {
				b, err := os.ReadFile(schemaFile)
				if err != nil {
					return fmt.Errorf("read schema file: %w", err)
				}
				if err := json.Unmarshal(b, &schema); err != nil {
					return fmt.Errorf("parse schema file: %w", err)
				}
			} else {
				return fmt.Errorf("schema file is required for synthesis")
			}

			controller := fanoutController(socketPath)
			fanoutOpts := orchestration.FanoutOptions{
				Prompt:  orchestration.BuildSyntheticDataPrompt(prompt, count, nil),
				Targets: parseFanoutTargets(targets, nil),
				ModelOptions: api.ModelOptions{
					ResponseSchema:  schema,
					ReasoningEffort: reasoningEffort,
				},
				EventBuffer: 1024,
			}

			res, err := orchestration.RunFanout(ctx, controller, fanoutOpts)
			if err != nil {
				return err
			}

			if len(res.Targets) == 0 || res.Targets[0].Error != "" {
				return fmt.Errorf("generation failed: %s", res.Targets[0].Error)
			}

			var outputData any
			if err := json.Unmarshal([]byte(res.Targets[0].Text), &outputData); err != nil {
				return fmt.Errorf("parse output: %w", err)
			}

			if outFormat == "ndjson" {
				if arr, ok := outputData.([]any); ok {
					for _, item := range arr {
						b, _ := json.Marshal(item)
						fmt.Println(string(b))
					}
				} else {
					b, _ := json.Marshal(outputData)
					fmt.Println(string(b))
				}
			} else {
				b, _ := json.MarshalIndent(outputData, "", "  ")
				fmt.Println(string(b))
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&count, "count", 5, "Number of items to generate")
	cmd.Flags().StringVar(&schemaFile, "schema", "", "JSON schema file to enforce")
	cmd.Flags().StringArrayVar(&targets, "target", []string{"openaicompatible=gpt-4o"}, "Target in the form backend=model")
	cmd.Flags().StringVar(&outFormat, "format", "ndjson", "Output format (json, ndjson)")
	cmd.Flags().StringVar(&socketPath, "socket-path", "", "Optional daemon socket path")
	cmd.Flags().StringVar(&reasoningEffort, "reasoning-effort", "", "Advanced model option for all targets")

	return cmd
}

func newDatasetGenerateSchemaCmd() *cobra.Command {
	var socketPath string
	var targets []string

	cmd := &cobra.Command{
		Use:   "generate-schema [description]",
		Short: "Generate a JSON schema based on a plain English description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			desc := args[0]

			schemaSchema := map[string]any{
				"type": "object",
				"properties": map[string]any{
					"$schema": map[string]any{"type": "string"},
					"type":    map[string]any{"type": "string"},
					"properties": map[string]any{
						"type":                 "object",
						"additionalProperties": true,
					},
					"required": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"required":             []string{"type", "properties"},
				"additionalProperties": true,
			}

			controller := fanoutController(socketPath)
			opts := orchestration.FanoutOptions{
				Prompt:  fmt.Sprintf("You are an expert data architect. Given the following description of a data structure, write a complete, strict JSON schema (Draft 7) for it. ALWAYS wrap the root properties inside an array so it can be generated as an array of items for NDJSON processing.\n\nDescription:\n%s", desc),
				Targets: parseFanoutTargets(targets, nil),
				ModelOptions: api.ModelOptions{
					ResponseSchema: schemaSchema,
				},
				EventBuffer: 1024,
			}

			res, err := orchestration.RunFanout(ctx, controller, opts)
			if err != nil {
				return err
			}

			if len(res.Targets) == 0 || res.Targets[0].Error != "" {
				return fmt.Errorf("generation failed: %s", res.Targets[0].Error)
			}

			var outputData any
			if err := json.Unmarshal([]byte(res.Targets[0].Text), &outputData); err != nil {
				return fmt.Errorf("parse schema output: %w", err)
			}

			b, _ := json.MarshalIndent(outputData, "", "  ")
			fmt.Println(string(b))

			return nil
		},
	}

	cmd.Flags().StringArrayVar(&targets, "target", []string{"openaicompatible=gpt-4o"}, "Target in the form backend=model")
	cmd.Flags().StringVar(&socketPath, "socket-path", "", "Optional daemon socket path")

	return cmd
}
