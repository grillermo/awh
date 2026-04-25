#!/usr/bin/env python3
from PIL import Image
import sys

input_path = sys.argv[1] if len(sys.argv) > 1 else "frame.jpg"
output_path = sys.argv[2] if len(sys.argv) > 2 else "display_crop.jpg"

img = Image.open(input_path)
w, h = img.size

# Black circular display with temperature number, lower-right of heater body
# Tuned for 640x480 Tapo frame
left   = int(w * 0.67)
top    = int(h * 0.65)
right  = int(w * 0.87)
bottom = int(h * 0.92)

crop = img.crop((left, top, right, bottom))
crop.save(output_path)
print(f"Cropped {left},{top} -> {right},{bottom} from {w}x{h} → saved to {output_path}")
