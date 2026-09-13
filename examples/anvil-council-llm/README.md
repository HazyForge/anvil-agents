# Anvil council LLM harness

Custom conversation image for Kind council turns. The Job reads
`ANVIL_AGENT_RUN_PROMPT_FILE` and `DEEPSEEK_API_KEY` (from harness
`envSecretRefs`, never from the API process) and prints
`ANVIL_COUNCIL_DECISION=` JSON. The API parses that decision; it does not
script conferral.

Build and load on Kind:

```bash
docker build -t anvil-agents-council-llm:dev examples/anvil-council-llm
kind load docker-image anvil-agents-council-llm:dev --name anvil-agents
```
