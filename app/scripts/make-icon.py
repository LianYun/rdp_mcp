"""生成应用图标源图（1024x1024 PNG），再由 `pnpm tauri icon` 派生各平台图标。

用法：python3 -I scripts/make-icon.py out.png
"""
import sys

from PIL import Image, ImageDraw

out = sys.argv[1]
S = 1024
img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
d = ImageDraw.Draw(img)
d.rounded_rectangle((64, 64, S - 64, S - 64), radius=220, fill=(37, 99, 235, 255))
# 显示器
d.rounded_rectangle((220, 250, 804, 640), radius=40, outline=(255, 255, 255, 255), width=44)
d.rectangle((470, 660, 554, 760), fill=(255, 255, 255, 255))
d.rounded_rectangle((350, 750, 674, 810), radius=24, fill=(255, 255, 255, 255))
# 光标
d.polygon([(470, 340), (470, 540), (520, 495), (560, 580), (600, 562), (560, 478), (625, 470)], fill=(255, 255, 255, 255))
img.save(out)
