.PHONY: icons

icons:
	ffmpeg -y -v error -i resources/favicon_source.png \
		-vf "scale=32:32:force_original_aspect_ratio=decrease,pad=32:32:(ow-iw)/2:(oh-ih)/2:color=0x00000000" \
		resources/favicon.ico
	ffmpeg -y -v error -i resources/favicon_source.png \
		-vf "scale=32:32:force_original_aspect_ratio=decrease,pad=32:32:(ow-iw)/2:(oh-ih)/2:color=0x00000000" \
		resources/favicon-32.png
