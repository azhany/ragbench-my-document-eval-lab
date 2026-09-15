"""RB-06/RB-07: the asynchronous source-to-searchable-revision pipeline."""
from datetime import datetime, timedelta, timezone

from airflow.sdk import dag, get_current_context, task

from ragbench.pipeline import run_stage, task_failure


def build_document_dag(dag_id):
    @dag(dag_id=dag_id, schedule=None, start_date=datetime(2025, 1, 1, tzinfo=timezone.utc),
         catchup=False, is_paused_upon_creation=False, max_active_runs=4,
         tags=["ragbench", "documents"],
         default_args={"retries": 2, "retry_delay": timedelta(seconds=10),
                       "execution_timeout": timedelta(minutes=10), "on_failure_callback": task_failure})
    def pipeline():
        @task
        def stage(name):
            context = get_current_context()
            run = context["dag_run"]
            run_stage(run.conf["job_id"], name, run.dag_id, run.run_id)

        previous = None
        for name in ("extract", "normalize", "chunk", "embed", "publish"):
            current = stage.override(task_id=name)(name)
            if previous is not None:
                previous >> current
            previous = current

    return pipeline()


document_ingestion = build_document_dag("document_ingestion")
document_reindex = build_document_dag("document_reindex")
