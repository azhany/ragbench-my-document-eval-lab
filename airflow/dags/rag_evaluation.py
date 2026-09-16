"""RB-14: the rag_evaluation DAG — orchestration only.

Airflow drives the Go API per case; Go owns the query pipeline, scoring and
persistence (Python must not become a second backend). The DAG run's conf
carries the durable eval run id, reused across ambiguous timeouts, so task
retries never duplicate results: case execution is idempotent per
(run_id, eval_case) server side, and finalize recomputes aggregate run
status from persisted result rows.
"""
from datetime import datetime, timedelta, timezone

from airflow.sdk import dag, get_current_context, task

from ragbench.evaluation import finalize_run, run_pending_cases


def build_rag_evaluation_dag(dag_id="rag_evaluation"):
    @dag(dag_id=dag_id, schedule=None,
         start_date=datetime(2025, 1, 1, tzinfo=timezone.utc),
         catchup=False, max_active_runs=2,
         tags=["ragbench", "evaluation"],
         default_args={"retries": 1, "retry_delay": timedelta(seconds=30),
                       "execution_timeout": timedelta(minutes=45)})
    def evaluation():
        @task
        def cases():
            conf = get_current_context()["dag_run"].conf
            return run_pending_cases(conf["run_id"])

        @task
        def finalize(run_status: dict):
            return finalize_run(run_status["id"])

        finalize(cases())

    return evaluation()


rag_evaluation = build_rag_evaluation_dag()
