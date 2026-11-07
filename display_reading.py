"""
What the Bosch Therm 5600 F display can show, and turning an OCR result into
one of those things.

Everything comes from the heater's manual ("Therm 5600 F – 6 720 890 046
(2022/03)", manual_5600f_03.2022.pdf). Any reading that is not on these lists
is treated as nothing read: a blank or dimmed display, the 188°C power-on
segment test after a reset, glare, or OCR noise. Only a confident reading of a
listed error code counts as an error, which is what triggers a reset.
"""

import re

# Table 17 "Problemas", pages 30–31.
ERROR_CODES = frozenset({
    "A0",  # inlet and outlet temperature sensors
    "A7",  # outlet water temperature sensor
    "C7",  # fan not working
    "CA",  # water flow above the maximum
    "CF",  # flue blocked / not enough air
    "E1",  # outlet sensor detected overheating
    "E2",  # inlet water temperature sensor
    "E9",  # thermal fuse or temperature limiter
    "EA",  # no flame detected
    "EC",  # flame lost during operation
    "EE",  # solenoid valve not connected
    "EF",  # natural-gas appliance on the wrong gas
    "F7",  # flame detected while the appliance is off
    "FA",  # gas valve fault
})

# Fig. 7 "Menús usuario" (page 15) and sections 4.6 and 8 (pages 16–17, 25).
# Shown while someone is using the menus, never an error.
MENU_CODES = frozenset(
    {"P4", "P9", "SA", "PP", "DH", "OD", "FH", "LP", "HP",
     "H0", "H1", "H2", "C0", "C1", "C2",
     "P8", "P6", "P5", "P3", "AA", "AP", "P0", "L0", "ON", "OF", "00"}
    | {f"{n}F" for n in range(1, 11)}   # 1F..10F: the last 10 errors
    | {f"{n}D" for n in range(1, 6)}    # 1D..5D: operating data
)

# RapidOCR's own confidence; below this the reading is noise. Measured on the
# 60 saved samples: real "45" frames scored >= 0.77, noise on dark or odd
# frames <= 0.36 (and 0.67 for one "8" on the 188 boot screen).
MIN_SCORE = 0.7

# A temperature is two digits, optionally followed by the unit the display
# draws beside it ("45°C"; OCR often drops the degree sign).
_TEMPERATURE = re.compile(r"(\d{2})(?:°?[CF])?")


def classify(text, score):
    """
    Returns (kind, value): kind is "temperature", "error", "menu" or "none",
    and value is the cleaned reading ("45", "E1", "P4") or "" for "none".
    """
    if score is None or score < MIN_SCORE:
        return "none", ""
    compact = re.sub(r"\s+", "", text or "").upper()
    if not compact:
        return "none", ""
    # Menus first: "00" (P5's ignition delay) would otherwise pass as a temperature.
    if compact in MENU_CODES:
        return "menu", compact
    if compact in ERROR_CODES:
        return "error", compact
    match = _TEMPERATURE.fullmatch(compact)
    if match:
        return "temperature", match.group(1)
    return "none", ""
