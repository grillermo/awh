#!/usr/bin/env python3
"""Tests for the OCR pre-processing step. Run: python -m unittest auto_reset_test"""

import unittest

import cv2
import numpy as np

from auto_reset import ENHANCE_MIN_SPREAD, enhance_frame


def mean_luma(frame):
    gray = frame if frame.ndim == 2 else cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
    return float(gray.mean())


class EnhanceFrameTest(unittest.TestCase):
    def test_preserves_shape_and_dtype(self):
        frame = np.full((200, 200, 3), 40, np.uint8)
        frame[80:120, 60:140] = 90
        out = enhance_frame(frame)
        self.assertEqual(out.shape, frame.shape)
        self.assertEqual(out.dtype, np.uint8)

    def test_pushes_dim_highlights_to_the_top_of_the_range(self):
        frame = np.full((200, 200, 3), 6, np.uint8)
        frame[80:120, 60:140] = 70  # dim digits on a dark panel
        out = cv2.cvtColor(enhance_frame(frame), cv2.COLOR_BGR2GRAY)
        self.assertGreaterEqual(np.percentile(out, 99), 200)

    def test_brightens_a_globally_dark_frame(self):
        # Low contrast and dark everywhere: nothing to anchor to white, so the
        # gamma stage has to carry the luminosity lift.
        frame = np.tile(np.linspace(2, 48, 200, dtype=np.uint8), (200, 1))
        frame = cv2.cvtColor(frame, cv2.COLOR_GRAY2BGR)
        self.assertGreater(mean_luma(enhance_frame(frame)), 3 * mean_luma(frame))

    def test_raises_contrast_of_a_dark_display(self):
        frame = np.full((200, 200, 3), 6, np.uint8)
        frame[80:120, 60:140] = 70
        before = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY).std()
        after = cv2.cvtColor(enhance_frame(frame), cv2.COLOR_BGR2GRAY).std()
        self.assertGreater(after, before)

    def test_flat_frames_are_left_alone(self):
        for value in (0, 128, 255):
            with self.subTest(value=value):
                frame = np.full((50, 50, 3), value, np.uint8)
                np.testing.assert_array_equal(enhance_frame(frame), frame)

    def test_skips_stretch_when_there_is_no_signal_to_recover(self):
        # Spread below the threshold is noise, not a dim display: the stretch
        # must not gain it up into something OCR reads as text.
        rng = np.random.default_rng(0)
        frame = (rng.random((200, 200, 3)) * (ENHANCE_MIN_SPREAD - 6) + 10).astype(np.uint8)
        out = enhance_frame(frame)
        self.assertLess(cv2.cvtColor(out, cv2.COLOR_BGR2GRAY).std(), 40)

    def test_accepts_single_channel_frames(self):
        frame = np.full((50, 50), 30, np.uint8)
        frame[10:20, 10:20] = 90
        self.assertEqual(enhance_frame(frame).shape, frame.shape)

    def test_transform_is_monotone(self):
        # Same ordering in, same ordering out: the step cannot invent structure.
        ramp = np.arange(256, dtype=np.uint8).reshape(1, 256)
        out = enhance_frame(np.repeat(ramp, 8, axis=0))[0]
        self.assertTrue(np.all(np.diff(out.astype(int)) >= 0))


if __name__ == "__main__":
    unittest.main()
