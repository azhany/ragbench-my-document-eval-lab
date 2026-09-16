"""RB-23: opt-in repeatable regression checks.

The default schedule is disabled. Set RAGBENCH_SCHEDULE_ENABLED=true and an
explicit RAGBENCH_SCHEDULE_CRON before creating the DAG schedule; this avoids
accidental provider spending in local Compose while retaining a real Airflow
execution path for an explicitly authorized environment.
"""
import os
from datetime import datetime, timedelta, timezone

from airflow.sdk import dag, get_current_context, task

from ragbench.scheduled import run_scheduled_check


def build_scheduled_regression_dag(dag_id="rag_scheduled_regression"):
    enabled = os.environ.get("RAGBENCH_SCHEDULE_ENABLED", "false").lower() == "true"
    schedule = os.environ.get("RAGBENCH_SCHEDULE_CRON") if enabled else None

    @dag(dag_id=dag_id, schedule=schedule,
         start_date=datetime(2025, 1, 1, tzinfo=timezone.utc), catchup=False,
         is_paused_upon_creation=not enabled, max_active_runs=1,
         tags=["ragbench", "regression"],
         default_args={"retries": 1, "retry_delay": timedelta(minutes=1),
                       "execution_timeout": timedelta(minutes=50)})
    def scheduled_regression():
        @task
        def run_check():
            conf = get_current_context()["dag_run"].conf or {}
            check_id = conf.get("check_id") or os.environ.get("RAGBENCH_SCHEDULE_CHECK_ID")
            if not check_id:
                raise ValueError(
                    "scheduled regression requires dag_run.conf.check_id or "
                    "RAGBENCH_SCHEDULE_CHECK_ID"
                )
            return run_scheduled_check(check_id)

        run_check()

    return scheduled_regression()


rag_scheduled_regression = build_scheduled_regression_dag()
