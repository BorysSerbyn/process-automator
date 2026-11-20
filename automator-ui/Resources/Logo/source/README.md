# Logo generator

Builds the logo (an hourglass drawn as one stroke, whose head runs into the status dot) from `hg.py` (the shape) and `build.py` (the SVGs: `AppIcon.svg`, `AppIcon-small.svg` for 16 and 32 px, and the menu bar `glyph.svg` + `dot.svg` in an 18×18 frame). `render.sh` then makes the iconset, `AppIcon.icns`, the 1024 PNG and the preview sheets.

```sh
cd automator-ui/Resources/Logo/source
python3 -m venv venv && ./venv/bin/pip install shapely numpy
brew install librsvg                      # rsvg-convert
swiftc iconsheet.swift -o iconsheet && swiftc menubar.swift -o menubar   # preview sheets
./render.sh .. --connected                # writes into Logo/ (the shipped set)
```

`--connected` is the shipped variant: the stroke's head joins the dot. Without it the dot has a gap on both sides. `build.py` takes a JSON object of shape overrides as its second argument (see `DEF` in `hg.py`).

The app ships `AppIcon.icns`, `glyph.svg` and `dot.svg` (`scripts/bundle.sh`); the indicator draws `glyph.svg` as a template image and tints `dot.svg` with the status colour.
