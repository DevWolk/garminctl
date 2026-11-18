# Embedded UI dependencies

These existing UI dependencies are served from the application instead of a CDN.
Native startup and reload therefore need no external script, stylesheet or font.
Map tiles still use the existing OpenStreetMap service when a map is viewed.

- Leaflet 1.9.4: unmodified [distribution](https://unpkg.com/leaflet@1.9.4/dist/),
  including its five PNG images. [BSD license](leaflet/LICENSE).
- Manrope and JetBrains Mono normal variable fonts: Google Fonts commit
  [7af0d90b4d532ffa4b041937b560c6df4e7b1e95](https://github.com/google/fonts/tree/7af0d90b4d532ffa4b041937b560c6df4e7b1e95/ofl).
  Original `Manrope[wght].ttf` and `JetBrainsMono[wght].ttf` are named without
  brackets locally. [Manrope OFL](fonts/Manrope-OFL.txt) and
  [JetBrains Mono OFL](fonts/JetBrainsMono-OFL.txt).

The fonts and Leaflet code are unchanged; `fonts.css` defines the local font URLs.
Go embeds this directory with the rest of the SPA. Builds do not download assets.
