# Evaluation Engine

Agentic Control exposes dataset evaluation through [`sigma-evals`](https://github.com/benjaminwestern/sigma-evals). The execution boundary remains the Agentic Control fanout/session layer, while the evaluation contract, judge parsing, G-Eval scoring, and judge-alignment metrics come from sigma-evals.

## Evaluation Modes

The engine supports two primary evaluation modes, exposed via the `--mode` flag:

1. **Strict JSON Evaluation (`evaluate`)**: The default mode. sigma-evals asks the judge for `{"score": <float>, "rationale": "<string>", "passed": <bool>}`, validates the response, and runs one repair turn for malformed JSON.
2. **Logprob Weighting (G-Eval) (`g_eval`)**: sigma-evals asks the judge for a single integer from 1 to 5, reads score-token `logprobs`, and computes the weighted continuous score. If provider logprobs are unavailable, the run fails explicitly instead of falling back to text parsing.

## Running Batch Evaluations

You can run evaluations offline against a JSON file containing an array of dataset items.

### Dataset Item Format
```json
[
  {
    "input_payload": "Translate 'hello' to French.",
    "target_output": "Bonjour"
  }
]
```

### CLI Usage
```bash
agent_control dataset eval run \
  --items ./dataset.json \
  --prompt "rubric-accuracy" \
  --target-model "openaicompatible=gpt-4o-mini" \
  --judge-model "openaicompatible=gpt-4o" \
  --mode "g_eval"
```
The orchestrator will:
1. Render the target prompt and run it through Agentic Control fanout.
2. Pass the resulting output and the `target_output` ground truth to sigma-evals using the selected judge model and rubric.
3. Output the results as JSON containing the score, rationale, latency, and cost.

## MCP & JSON-RPC

The evaluation engine is fully exposed over MCP and JSON-RPC.

- **MCP Tool**: `run_batch_evaluation`
  - Accepts `prompt`, `items` (array of input/truth pairs), `target` model, `judge` model, and `mode`.
- **JSON-RPC**: `session/new` with `response_schema` natively handles strict extraction. For full evaluations, downstream clients can utilize the orchestration JSON-RPC endpoints.
