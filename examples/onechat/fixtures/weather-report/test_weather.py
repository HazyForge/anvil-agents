import unittest

from weather import c_to_f, summary


class WeatherTest(unittest.TestCase):
    def test_c_to_f(self):
        self.assertEqual(c_to_f(100), 212)

    def test_summary(self):
        result = summary([("mon", 10.0), ("tue", 20.0)])
        self.assertEqual(result["days"], 2)
        self.assertEqual(result["mean_c"], 15.0)


if __name__ == "__main__":
    unittest.main()
