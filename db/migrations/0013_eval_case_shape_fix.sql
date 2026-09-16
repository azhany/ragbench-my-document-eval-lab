-- RB-13 follow-up: expected_evidence is stored as a single object with the
-- document-id list and their source labels; relax the earlier array-shape
-- check to match the persisted form.
ALTER TABLE eval_cases
	DROP CONSTRAINT eval_cases_evidence_shape_check,
	ADD CONSTRAINT eval_cases_evidence_shape_check CHECK (
		jsonb_typeof(expected_evidence) = 'object');
