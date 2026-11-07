#!/usr/bin/env python3
"""Run: python -m unittest display_reading_test"""

import unittest

from display_reading import ERROR_CODES, MIN_SCORE, classify

SURE = 0.95


class ClassifyTest(unittest.TestCase):
    def test_temperatures(self):
        self.assertEqual(classify("45", SURE), ("temperature", "45"))
        self.assertEqual(classify("45°C", SURE), ("temperature", "45"))
        self.assertEqual(classify("45C", SURE), ("temperature", "45"))
        self.assertEqual(classify(" 60 ", SURE), ("temperature", "60"))

    def test_every_manual_error_code_is_an_error(self):
        for code in ERROR_CODES:
            self.assertEqual(classify(code, SURE), ("error", code))
        self.assertEqual(classify("e1", SURE), ("error", "E1"))

    def test_menu_codes_are_not_errors(self):
        for code in ("P4", "SA", "oF", "on", "10F", "3D", "C1", "00"):
            self.assertEqual(classify(code, SURE)[0], "menu", code)

    def test_low_confidence_is_nothing(self):
        self.assertEqual(classify("45", MIN_SCORE - 0.01), ("none", ""))
        self.assertEqual(classify("E1", 0.3), ("none", ""))
        self.assertEqual(classify("45", None), ("none", ""))

    def test_anything_not_in_the_manual_is_nothing(self):
        # Noise seen on dark frames and the 188°C power-on test pattern.
        for text in ("恒", "福", "鸭", "C", "1", "8", "188", "451", "4", "UG", "W", ""):
            self.assertEqual(classify(text, SURE), ("none", ""), text)


if __name__ == "__main__":
    unittest.main()
