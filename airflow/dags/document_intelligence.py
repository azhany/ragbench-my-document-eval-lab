"""RB-27--RB-31: bounded financial document intelligence workflow."""
from datetime import datetime, timedelta, timezone

from airflow.sdk import dag, get_current_context, task

from ragbench.intelligence import ORDERED_STAGES, run_stage, task_failure


@dag(
    dag_id="document_intelligence",
    schedule=None,
    start_date=datetime(2025, 1, 1, tzinfo=timezone.utc),
    catchup=False,
    is_paused_upon_creation=False,
    max_active_runs=4,
    tags=["ragbench", "document-intelligence", "sprint-7"],
    default_args={
        "retry_delay": timedelta(seconds=10),
        "execution_timeout": timedelta(minutes=5),
        "on_failure_callback": task_failure,
    },
)
def document_intelligence_pipeline():
    previous = None
    for name in ORDERED_STAGES:
        # Go owns one bounded provider retry for structured extraction and
        # summary. Schema/financial validation is deterministic and must not
        # be replayed as a model retry; extraction alone may retry a transient
        # OCR/process failure at the orchestration boundary.
        @task(task_id=name, retries=1 if name == ORDERED_STAGES[0] else 0)
        def stage(stage_name=name):
            context = get_current_context()
            dag_run = context["dag_run"]
            run_stage(dag_run.conf["analysis_id"], stage_name,
                      dag_run.dag_id, dag_run.run_id)

        current = stage()
        if previous is not None:
            previous >> current
        previous = current


document_intelligence = document_intelligence_pipeline()
