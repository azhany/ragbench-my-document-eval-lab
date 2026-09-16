"""RB-18: the rag_parameter_sweep DAG — orchestration only.

The sweep's persisted state machine lives in Go (experiment identity,
expanded combination matrix, per-combination immutable config identities,
document_reindex resolution, and eval run links). This DAG repeatedly performs
one idempotent advance step against the Go API until the experiment reports a
terminal state, so task retries reuse created identities instead of
duplicating logical experiments, runs, or index revisions.
"""
from datetime import datetime, timedelta, timezone

from airflow.sdk import dag, get_current_context, task

from ragbench.evaluation import APIError, TERMINAL_STATES, advance_one_step


MAX_STEPS = 1000


def build_rag_parameter_sweep_dag(dag_id="rag_parameter_sweep"):
    @dag(dag_id=dag_id, schedule=None,
         start_date=datetime(2025, 1, 1, tzinfo=timezone.utc),
         catchup=False, max_active_runs=1,
         tags=["ragbench", "experiments"],
         default_args={"retries": 1, "retry_delay": timedelta(minutes=1),
                       "execution_timeout": timedelta(minutes=90)})
    def sweep():
        @task
        def drive():
            experiment_id = get_current_context()["dag_run"].conf["experiment_id"]
            # Idempotent bounded loop: every step is a safe re-entry and the
            # experiment converges. A failed state is surfaced as a task
            # failure so Airflow observability shows it too.
            result = {"state": ""}
            for _ in range(MAX_STEPS):
                result = advance_one_step(experiment_id)
                if result["state"] in TERMINAL_STATES:
                    break
            else:
                raise APIError("sweep did not converge; inspect /api/v1/experiments/"
                               + experiment_id)
            if result["state"] == "failed":
                raise APIError("experiment ended failed; inspect /api/v1/experiments/"
                               + experiment_id)
            return result

        drive()

    return sweep()


rag_parameter_sweep = build_rag_parameter_sweep_dag()
