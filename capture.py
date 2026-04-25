import cv2
import sys

RTSP_URL = "rtsp://grillermo:123456789@192.168.1.21/stream2"
OUTPUT = "frame.jpg"

cap = cv2.VideoCapture(RTSP_URL)
if not cap.isOpened():
    print("Failed to open stream", file=sys.stderr)
    sys.exit(1)

ret, frame = cap.read()
cap.release()

if not ret:
    print("Failed to read frame", file=sys.stderr)
    sys.exit(1)

cv2.imwrite(OUTPUT, frame)
print(f"Saved {OUTPUT}")
