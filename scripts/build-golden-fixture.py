#!/usr/bin/env python3
"""Renders db/fixtures/golden_dataset_v1.json: 20 reviewed cases over the
three demonstration source documents. Expected evidence names the source
filename; scripts/seed-golden.sh maps filenames to live document ids at
import time. Filenames (not chunk UUIDs) keep the relevance unit stable
across chunk configurations."""
import json

FG = "finance-guidelines.txt"
DS = "data-sharing-policy.txt"
HR = "hr-practice-handbook.txt"

fg_cases = [
    ("fg-01", "How many approval stages does an expenditure request go through?",
     "Three: technical review by the division cost officer, certification by the head of finance, and counter-signature by the controlling officer."),
    ("fg-02", "When does the ministry tender board convene for large requests?",
     "For requests above RM 500,000 the tender board must convene within five working days of technical review."),
    ("fg-03", "What is the direct-purchase threshold and what is required between it and RM 100,000?",
     "Direct purchase up to RM 5,000; between RM 5,000 and RM 100,000, at least three quotations are required."),
    ("fg-04", "When is open tender mandatory?",
     "Above RM 100,000 open tender applies."),
    ("fg-05", "Can emergency procurement bypass open tender, and what must be reported?",
     "Yes, with written justification; the commitment must be reported to the audit committee within 14 days."),
    ("fg-06", "What is the annual budget submission deadline and what happens to a late submission?",
     "Two months before fiscal year start; a late submission is returned once for correction, then escalated to the controlling officer."),
    ("fg-07", "Does the guidelines require a board meeting for any expenditure request?",
     "Yes: for requests above RM 500,000 the ministry tender board must convene within five working days of technical review."),
]
ds_cases = [
    ("ds-01", "What must a data sharing agreement contain at minimum?",
     "The dataset owner, permitted purposes, and a review date no later than 24 months after signature."),
    ("ds-02", "How are datasets containing personal data handled before sharing?",
     "They must first pass an anonymisation review; de-identified logs keep latencies and totals but drop identifiers, free-text remarks, and device fingerprints."),
    ("ds-03", "How long do shared extracts remain valid?",
     "90 days after the review date unless the agreement is renewed; expired extracts are deleted and the deletion recorded."),
    ("ds-04", "How often is the register of active agreements reviewed?",
     "Quarterly."),
    ("ds-05", "What is the review date limit on a data sharing agreement?",
     "No later than 24 months after signature."),
    ("ds-06", "What happens to an expired extract?",
     "It is deleted and the deletion is recorded in the register."),
    ("ds-07", "Does the data sharing policy regulate personal data between departments directly?",
     "No; personal data must pass anonymisation review first."),
]
hr_cases = [
    ("hr-01", "How many paid leave days per year are staff entitled to?",
     "21 paid leave days per year, with up to 5 carried forward."),
    ("hr-02", "How early must a leave application be lodged?",
     "At least 12 working days before the first day of leave."),
    ("hr-03", "When is performance reviewed during probation?",
     "Twice, at month two and month four of the six-month probation."),
    ("hr-04", "Can carried-forward leave be paid out in cash?",
     "No; carried-forward days are credited to work only."),
    ("hr-05", "What additional approval does leave longer than 10 consecutive days need?",
     "Counter-approval by the department head."),
    ("hr-06", "How many paid sick leave days apply during probation?",
     "Up to 10."),
]

cases = []
for source, items in ((FG, fg_cases), (DS, ds_cases), (HR, hr_cases)):
    for key, question, reference in items:
        cases.append({
            "case_key": key,
            "question": question,
            "reference_answer": reference,
            "expected_source_name": source,
        })

payload = {
    "name": "golden-dataset-v1",
    "description": "Reviewed 20-case demonstration golden dataset over the three RAGbench-MY demonstration sources (see db/fixtures/golden_dataset_v1_README.txt). Reference unit: document.",
    "cases": cases,
}
with open("db/fixtures/golden_dataset_v1.json", "w") as f:
    json.dump(payload, f, indent=2, ensure_ascii=False)
    f.write("\n")
print(f"wrote {len(cases)} cases")
