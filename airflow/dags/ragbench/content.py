"""Deterministic extraction and character chunking, independent of Airflow/DB."""
import hashlib
import re
import unicodedata
import uuid
import zipfile
from pathlib import Path


class IngestionError(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


def extract(path, mime_type, checksum, upload_dir, max_chars, max_expanded_bytes):
    source = Path(path).resolve()
    if source.parent != Path(upload_dir).resolve():
        raise IngestionError("storage_failed", "Source is outside the upload directory")
    try:
        data = source.read_bytes()
    except OSError as exc:
        raise IngestionError("storage_failed", "Source bytes are unavailable") from exc
    if hashlib.sha256(data).hexdigest() != checksum:
        raise IngestionError("source_changed", "Source checksum differs from the saved revision")
    sections = []
    total = 0

    def append(text, location):
        nonlocal total
        total += len(text)
        if total > max_chars:
            raise IngestionError("extraction_limit", "Extracted content exceeds MAX_EXTRACTED_CHARS")
        sections.append({"text": text.replace("\x00", ""), "location": location})

    try:
        if mime_type == "text/plain":
            append(data.decode("utf-8-sig"), {"source": "text", "section": 1})
        elif mime_type == "application/pdf":
            from pypdf import PdfReader
            reader = PdfReader(source)
            if reader.is_encrypted:
                raise IngestionError("encrypted_document", "Encrypted PDFs are not supported")
            for number, page in enumerate(reader.pages, 1):
                append(page.extract_text() or "", {"page": number})
        elif mime_type == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
            from docx import Document
            from docx.table import Table
            if data.startswith(bytes.fromhex("d0cf11e0a1b11ae1")):
                raise IngestionError("encrypted_document", "Encrypted/legacy Office containers are not supported")
            with zipfile.ZipFile(source) as archive:
                if sum(item.file_size for item in archive.infolist()) > max_expanded_bytes:
                    raise IngestionError("extraction_limit", "Expanded DOCX exceeds MAX_DOCX_EXPANDED_BYTES")
            document = Document(source)
            section = None
            for number, block in enumerate(document.iter_inner_content(), 1):
                if isinstance(block, Table):
                    for row_number, row in enumerate(block.rows, 1):
                        append("\t".join(cell.text for cell in row.cells),
                               {"block": number, "table_row": row_number, "section": section})
                else:
                    if block.style and block.style.name.startswith("Heading"):
                        section = block.text
                    append(block.text, {"paragraph": number, "section": section})
        else:
            raise IngestionError("unsupported_format", "Unsupported source format")
    except IngestionError:
        raise
    except Exception as exc:
        # Parser exceptions can include source text; expose only the class.
        raise IngestionError("corrupt_document", f"Cannot extract document ({type(exc).__name__})") from exc
    if not any(normalize_text(item["text"]) for item in sections):
        code = "image_only_or_empty" if mime_type == "application/pdf" else "empty_content"
        raise IngestionError(code, "No useful text was extracted; OCR is not supported")
    return sections


def normalize_text(text):
    # Remove NUL/control/format characters before PostgreSQL persistence.
    text = unicodedata.normalize("NFC", text)
    text = "".join(c if c.isspace() or not unicodedata.category(c).startswith("C") else "" for c in text)
    return re.sub(r"\s+", " ", text).strip()


def normalize(sections):
    parts, locations = [], []
    offset = 0
    for section in sections:
        text = normalize_text(section["text"])
        if not text:
            continue
        if parts:
            offset += 1  # one newline separates source blocks
        locations.append({**section["location"], "start": offset, "end": offset + len(text)})
        parts.append(text)
        offset += len(text)
    if not parts:
        raise IngestionError("empty_content", "Normalization produced no useful text")
    return {"text": "\n".join(parts), "locations": locations}


def chunk(normalized, revision_id, size, overlap):
    if size < 1 or overlap < 0 or overlap >= size:
        raise IngestionError("invalid_chunk_config", "Chunk overlap must be smaller than positive chunk size")
    text = normalized["text"]
    chunks = []
    for start in range(0, len(text), size - overlap):
        end = min(start + size, len(text))
        content = text[start:end]
        if content.strip():
            index = len(chunks)
            chunks.append({
                "id": str(uuid.uuid5(uuid.UUID(revision_id), str(index))),
                "index": index, "content": content,
                "metadata": {"unit": "unicode_characters", "start": start, "end": end,
                             "locations": [loc for loc in normalized["locations"]
                                           if loc["start"] < end and loc["end"] > start]},
            })
        if end == len(text):
            break  # no redundant trailing overlap-only chunk
    if not chunks:
        raise IngestionError("empty_content", "Chunking produced no useful text")
    return chunks
