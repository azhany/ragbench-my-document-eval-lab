# Synthetic financial-document fixtures

These files are redistributable, synthetic examples for Sprint 7. They do not
contain customer invoices, personal data, credentials, or private financial
documents.

`financial_dataset_v1.json` is the labeled set used by the deterministic
financial evaluator in `backend/internal/documentintelligence/evaluation.go`.
The labels deliberately separate normalized field extraction from validation
rule detection:

- `invoice_valid.txt` is a clean invoice expected to pass.
- `receipt_missing_total.txt` has a receipt with no evidenced total; the
  missing important field is expected to be detected.
- `invoice_inconsistent.txt` contains intentionally inconsistent line,
  subtotal, and total values; the expected rule IDs are recorded in the set.

`corrupt.pdf` and `corrupt.png` are intentionally unreadable bytes. The
existing Airflow content tests also generate a valid text-bearing PDF, a
blank/image-only PDF, a valid PNG OCR path, and corrupt input at runtime. An
unsupported extension can be exercised with any file whose name ends in
`.html` or `.exe`; the API rejects it before persistence.

All fixture paths in the labeled set are relative to this directory.
