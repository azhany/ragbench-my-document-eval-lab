# Test Plan

## Unit tests

### Retrieval
- hybrid score ordering
- top-k truncation
- no-result behavior

### Cost
- token cost calculation
- unknown model profile behavior

### Evaluation
- Recall@K
- MRR
- regression threshold evaluation

## Integration tests

- upload → queued document
- processed document → chunks available
- chat → citations + trace persisted
- eval run → aggregate metrics persisted
- failed provider request → classified error trace

## One important failure-mode demo

Intentionally configure a poor retrieval setup, for example:
- very small chunk size
- vector-only retrieval
- low top-k

Run the same dataset and show:
- Recall@K drops
- answer faithfulness/relevance changes
- trace still identifies a healthy model call
- comparison page flags a regression

This makes the PoC tell a stronger engineering story than a normal happy-path chatbot.
