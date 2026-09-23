# Browser media delivery fixtures

Small, procedurally generated originals and separate public previews. No user content, third-party assets or external URLs are used. The browser tests upload these files through the application, publish a real listing, then verify public playback, paid delivery bytes, Range requests and refund revocation.

All images/video are 160 × 90: the original is blue, the preview yellow. Video is one second of H.264 baseline, 10 fps, yuv420p, MP4 faststart. Audio is one second of mono 44.1 kHz sine: original 440 Hz, preview 660 Hz; WAV PCM and MP3 at 64 kbps. Text is generated directly by the test.

To recreate with FFmpeg, use `color=c=blue:s=160x90:r=10:d=1` (or yellow) as a lavfi input. Export PNG/JPEG with one frame; MP4 uses `-c:v libx264 -profile:v baseline -pix_fmt yuv420p -movflags +faststart`. Audio uses lavfi `sine=frequency=440:sample_rate=44100:duration=1` (or 660), `-ac 1`, and `-c:a libmp3lame -b:a 64k` for MP3. Fixtures are committed so running CI does not require FFmpeg.
