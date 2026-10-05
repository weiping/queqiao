"""Tests for the Jev latency probe (latency.py)."""

import unittest

from latency import percentile


class PercentileTest(unittest.TestCase):
    def test_percentile(self):
        xs = [float(i) for i in range(1, 101)]
        self.assertEqual(percentile(xs, 50), 50.0)
        self.assertEqual(percentile(xs, 95), 95.0)


if __name__ == "__main__":
    unittest.main()
