FROM apache/airflow:3.0.6
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends tesseract-ocr \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
USER airflow
COPY requirements.txt /requirements-ragbench.txt
RUN pip install --no-cache-dir "apache-airflow==3.0.6" -r /requirements-ragbench.txt
