#!/usr/bin/env python3
"""Run: python -m unittest ocr_samples_test"""

import json
import os
import random
import tempfile
import unittest

import numpy as np

import ocr_samples
from ocr_samples import SampleCollector, classify


class Clock:
    def __init__(self, t=1_000_000.0):
        self.t = t

    def __call__(self):
        return self.t


def frames():
    return np.zeros((20, 20, 3), np.uint8), np.zeros((200, 200, 3), np.uint8)


class ClassifyTest(unittest.TestCase):
    def test_kinds(self):
        self.assertEqual(classify(""), "empty")
        self.assertEqual(classify("  "), "empty")
        self.assertEqual(classify("45"), "number")
        self.assertEqual(classify("4 5"), "number")
        self.assertEqual(classify("5"), "digit")
        self.assertEqual(classify(" 6 "), "digit")
        self.assertEqual(classify("E1"), "text")
        self.assertEqual(classify("4S"), "text")
        self.assertEqual(classify("hello"), "text")


class CollectorTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.clock = Clock()

    def collector(self, rng_value=0.0, max_bytes=ocr_samples.MAX_BYTES):
        rng = random.Random()
        rng.random = lambda: rng_value
        return SampleCollector(root=self.dir, rng=rng, clock=self.clock, max_bytes=max_bytes)

    def test_never_saves_numbers(self):
        self.assertIsNone(self.collector().maybe_save("45", *frames()))

    def test_saves_single_digits_in_their_own_folder(self):
        c = self.collector()
        rec = c.maybe_save("5", *frames())
        self.assertEqual(rec["kind"], "digit")
        self.assertIn(os.path.join(self.dir, "digit"), rec["ocr"])
        self.assertIsNotNone(c.maybe_save("6", *frames()))
        self.assertIsNone(c.maybe_save("5", *frames()))  # too soon

    def test_saves_each_distinct_text_and_both_images(self):
        c = self.collector()
        rec = c.maybe_save("E1", *frames())
        self.assertTrue(os.path.exists(rec["raw"]))
        self.assertTrue(os.path.exists(rec["ocr"]))
        self.assertIsNotNone(c.maybe_save("ER", *frames()))

    def test_caps_and_spaces_repeats_of_the_same_text(self):
        c = self.collector()
        self.assertIsNotNone(c.maybe_save("E1", *frames()))
        self.assertIsNone(c.maybe_save("E1", *frames()))  # too soon
        for _ in range(ocr_samples.TEXT_MAX_PER_TEXT - 1):
            self.clock.t += ocr_samples.TEXT_MIN_INTERVAL_S
            self.assertIsNotNone(c.maybe_save("E1", *frames()))
        self.clock.t += ocr_samples.TEXT_MIN_INTERVAL_S
        self.assertIsNone(c.maybe_save("E1", *frames()))  # cap reached

    def test_empty_is_random_and_rate_limited(self):
        self.assertIsNone(self.collector(rng_value=0.99).maybe_save("", *frames()))
        c = self.collector(rng_value=0.0)
        self.assertIsNotNone(c.maybe_save("", *frames()))
        self.assertIsNone(c.maybe_save("", *frames()))
        self.clock.t += ocr_samples.EMPTY_MIN_INTERVAL_S
        self.assertIsNotNone(c.maybe_save("", *frames()))

    def test_stops_saving_once_the_disk_budget_is_used(self):
        c = self.collector(max_bytes=1)
        self.assertIsNotNone(c.maybe_save("E1", *frames()))
        self.assertIsNone(c.maybe_save("ER", *frames()))
        # A restart measures what is already on disk.
        self.assertIsNone(self.collector(max_bytes=1).maybe_save("XY", *frames()))
        self.assertIsNotNone(self.collector().maybe_save("XY", *frames()))

    def test_restart_remembers_saved_texts(self):
        c = self.collector()
        for _ in range(ocr_samples.TEXT_MAX_PER_TEXT):
            c.maybe_save("E1", *frames())
            self.clock.t += ocr_samples.TEXT_MIN_INTERVAL_S
        self.assertIsNone(self.collector().maybe_save("E1", *frames()))
        with open(os.path.join(self.dir, "index.jsonl")) as fh:
            self.assertEqual(len([json.loads(l) for l in fh]), ocr_samples.TEXT_MAX_PER_TEXT)


class BrowseAndSelectTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.clock = Clock()

    def test_lists_samples_newest_first_with_stable_ids(self):
        c = SampleCollector(root=self.dir, clock=self.clock)
        c.maybe_save("E1", *frames())
        self.clock.t += 5
        c.maybe_save("5", *frames())
        samples = ocr_samples.list_samples(self.dir)
        self.assertEqual([s["ocr_text"] for s in samples], ["5", "E1"])
        self.assertTrue(samples[1]["id"].startswith("text/"))
        self.assertTrue(samples[1]["id"].endswith("_E1"))
        self.assertEqual(samples[1]["ocr_image"], samples[1]["id"] + "_ocr.png")

    def test_skips_samples_whose_images_were_deleted(self):
        rec = SampleCollector(root=self.dir, clock=self.clock).maybe_save("E1", *frames())
        os.remove(rec["ocr"])
        self.assertEqual(ocr_samples.list_samples(self.dir), [])

    def test_selection_round_trip(self):
        import sqlite3
        conn = sqlite3.connect(":memory:")
        self.assertEqual(ocr_samples.load_selection(conn), set())
        ocr_samples.update_selection(conn, select=["a", "b", "c"])
        ocr_samples.update_selection(conn, select=["a"], deselect=["b"])
        self.assertEqual(ocr_samples.load_selection(conn), {"a", "c"})


if __name__ == "__main__":
    unittest.main()
