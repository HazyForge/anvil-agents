"""Summarise a week of temperature readings from a CSV file."""
import csv
import sys


def c_to_f(celsius):
    return celsius * 9 / 5 + 32


def load(path):
    with open(path, newline="") as handle:
        return [(row["day"], float(row["celsius"])) for row in csv.DictReader(handle)]


def summary(readings):
    temps = [celsius for _, celsius in readings]
    return {
        "days": len(temps),
        "min_c": min(temps),
        "max_c": max(temps),
        "mean_c": sum(temps) / len(temps),
    }


def main(argv):
    readings = load(argv[1])
    result = summary(readings)
    print(f"{result['days']} days, {result['min_c']:.1f} to {result['max_c']:.1f} C, mean {result['mean_c']:.1f} C")


if __name__ == "__main__":
    main(sys.argv)
