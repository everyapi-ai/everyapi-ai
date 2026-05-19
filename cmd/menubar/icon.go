package main

// The procedural icon used to live here; M13 moved IconState +
// renderIcon into internal/menubar/icon.go so the controller can
// switch variants on state changes (signed-out / signed-in / alert)
// via the menuView interface. This file is intentionally empty
// except for the package declaration so build-macos.sh still finds
// a Go source in the directory without any stale icon code.
