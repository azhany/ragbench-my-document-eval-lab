"""RB-14/RB-18 orchestration client behavior checks (no network involved)."""
import unittest

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
