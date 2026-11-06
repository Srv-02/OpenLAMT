//go:build linux

package ui

import "github.com/go-gl/glfw/v3.4/glfw"

// waylandPlatform reports whether GLFW runs on Wayland (scan codes are raw evdev codes) rather
// than X11 (evdev + 8). Fyne decides the platform at start (loop_desktop.go:21-36), so this is
// only asked once the app runs; before glfw.Init the binding panics, which counts as X11.
func waylandPlatform() (wayland bool) {
	defer func() {
		if recover() != nil {
			wayland = false
		}
	}()
	return glfw.GetPlatform() == glfw.PlatformWayland
}
