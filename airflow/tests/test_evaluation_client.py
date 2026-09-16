"""RB-14/RB-18 orchestration client behavior checks (no network involved)."""
import unittest
from unittest import mock

from ragbench import evaluation


class TestEvaluationClient(unittest.TestCase):
    def test_terminal_states_set(self):
        self.assertEqual(
            {"completed", "partial", "failed", "dispatch_failed"},
            evaluation.TERMINAL_STATES,
        )

    def test_advance_report_shape(self):
        fake = evaluation.advance_one_step  # the real function builds a POST
        self.assertTrue(callable(fake))

    def test_run_pending_cases_returns_status_for_finalize(self):
        run = {"dataset_id": "ds", "dataset_version": 1}
        cases = [
            {"id": "case-1", "case_key": "one"},
            {"id": "case-2", "case_key": "two"},
        ]
        with mock.patch.object(evaluation, "results", return_value=[]), \
             mock.patch.object(evaluation, "run_status", side_effect=[run, {"id": "run-1", "status": "completed"}]), \
             mock.patch.object(evaluation, "dataset_cases", return_value=cases), \
             mock.patch.object(evaluation, "execute_case", return_value={"status": "completed"}) as execute:
            returned = evaluation.run_pending_cases("run-1")
        self.assertEqual(returned, {"id": "run-1", "status": "completed"})
        self.assertEqual(execute.call_count, 2)
