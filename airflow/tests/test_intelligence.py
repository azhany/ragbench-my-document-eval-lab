import unittest

from ragbench.intelligence import extraction_descriptor


class IntelligenceTests(unittest.TestCase):
    def test_extraction_descriptor_matches_parser(self):
        self.assertEqual(extraction_descriptor("application/pdf"),
                         ("pdf_text", "pypdf-text-v1"))
        self.assertEqual(extraction_descriptor("image/jpeg"),
                         ("image_ocr", "tesseract-ocr-v1"))
        self.assertEqual(extraction_descriptor("image/png"),
                         ("image_ocr", "tesseract-ocr-v1"))
        self.assertEqual(extraction_descriptor("text/plain"),
                         ("document_text", "document-parser-v1"))
        self.assertEqual(extraction_descriptor(
            "application/vnd.openxmlformats-officedocument.wordprocessingml.document"),
            ("document_text", "document-parser-v1"))


if __name__ == "__main__":
    unittest.main()
