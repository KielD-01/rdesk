// Package input injects mouse and keyboard events on the host.
//
// Keys arrive as browser KeyboardEvent.code values (physical key positions),
// so the host's own keyboard layout decides which character is typed, just
// like with a local keyboard.
package input

// key holds the platform codes for one physical key.
//
//	evdev: Linux input event code (X11 keycode = evdev + 8)
//	win:   PC/AT set-1 scan code; ext=true adds the 0xE0 prefix
//	mac:   macOS virtual key code (kVK_*), noMac if none
type key struct {
	evdev uint16
	win   uint16
	ext   bool
	mac   uint16
}

const noMac = 0xFFFF

var keys = map[string]key{
	"Escape": {1, 0x01, false, 0x35},
	"Digit1": {2, 0x02, false, 0x12}, "Digit2": {3, 0x03, false, 0x13}, "Digit3": {4, 0x04, false, 0x14},
	"Digit4": {5, 0x05, false, 0x15}, "Digit5": {6, 0x06, false, 0x17}, "Digit6": {7, 0x07, false, 0x16},
	"Digit7": {8, 0x08, false, 0x1A}, "Digit8": {9, 0x09, false, 0x1C}, "Digit9": {10, 0x0A, false, 0x19},
	"Digit0": {11, 0x0B, false, 0x1D},
	"Minus":  {12, 0x0C, false, 0x1B}, "Equal": {13, 0x0D, false, 0x18},
	"Backspace": {14, 0x0E, false, 0x33}, "Tab": {15, 0x0F, false, 0x30},
	"KeyQ": {16, 0x10, false, 0x0C}, "KeyW": {17, 0x11, false, 0x0D}, "KeyE": {18, 0x12, false, 0x0E},
	"KeyR": {19, 0x13, false, 0x0F}, "KeyT": {20, 0x14, false, 0x11}, "KeyY": {21, 0x15, false, 0x10},
	"KeyU": {22, 0x16, false, 0x20}, "KeyI": {23, 0x17, false, 0x22}, "KeyO": {24, 0x18, false, 0x1F},
	"KeyP":        {25, 0x19, false, 0x23},
	"BracketLeft": {26, 0x1A, false, 0x21}, "BracketRight": {27, 0x1B, false, 0x1E},
	"Enter": {28, 0x1C, false, 0x24}, "ControlLeft": {29, 0x1D, false, 0x3B},
	"KeyA": {30, 0x1E, false, 0x00}, "KeyS": {31, 0x1F, false, 0x01}, "KeyD": {32, 0x20, false, 0x02},
	"KeyF": {33, 0x21, false, 0x03}, "KeyG": {34, 0x22, false, 0x05}, "KeyH": {35, 0x23, false, 0x04},
	"KeyJ": {36, 0x24, false, 0x26}, "KeyK": {37, 0x25, false, 0x28}, "KeyL": {38, 0x26, false, 0x25},
	"Semicolon": {39, 0x27, false, 0x29}, "Quote": {40, 0x28, false, 0x27}, "Backquote": {41, 0x29, false, 0x32},
	"ShiftLeft": {42, 0x2A, false, 0x38}, "Backslash": {43, 0x2B, false, 0x2A},
	"KeyZ": {44, 0x2C, false, 0x06}, "KeyX": {45, 0x2D, false, 0x07}, "KeyC": {46, 0x2E, false, 0x08},
	"KeyV": {47, 0x2F, false, 0x09}, "KeyB": {48, 0x30, false, 0x0B}, "KeyN": {49, 0x31, false, 0x2D},
	"KeyM":  {50, 0x32, false, 0x2E},
	"Comma": {51, 0x33, false, 0x2B}, "Period": {52, 0x34, false, 0x2F}, "Slash": {53, 0x35, false, 0x2C},
	"ShiftRight": {54, 0x36, false, 0x3C}, "NumpadMultiply": {55, 0x37, false, 0x43},
	"AltLeft": {56, 0x38, false, 0x3A}, "Space": {57, 0x39, false, 0x31}, "CapsLock": {58, 0x3A, false, 0x39},
	"F1": {59, 0x3B, false, 0x7A}, "F2": {60, 0x3C, false, 0x78}, "F3": {61, 0x3D, false, 0x63},
	"F4": {62, 0x3E, false, 0x76}, "F5": {63, 0x3F, false, 0x60}, "F6": {64, 0x40, false, 0x61},
	"F7": {65, 0x41, false, 0x62}, "F8": {66, 0x42, false, 0x64}, "F9": {67, 0x43, false, 0x65},
	"F10":     {68, 0x44, false, 0x6D},
	"NumLock": {69, 0x45, true, 0x47}, "ScrollLock": {70, 0x46, false, noMac},
	"Numpad7": {71, 0x47, false, 0x59}, "Numpad8": {72, 0x48, false, 0x5B}, "Numpad9": {73, 0x49, false, 0x5C},
	"NumpadSubtract": {74, 0x4A, false, 0x4E},
	"Numpad4":        {75, 0x4B, false, 0x56}, "Numpad5": {76, 0x4C, false, 0x57}, "Numpad6": {77, 0x4D, false, 0x58},
	"NumpadAdd": {78, 0x4E, false, 0x45},
	"Numpad1":   {79, 0x4F, false, 0x53}, "Numpad2": {80, 0x50, false, 0x54}, "Numpad3": {81, 0x51, false, 0x55},
	"Numpad0": {82, 0x52, false, 0x52}, "NumpadDecimal": {83, 0x53, false, 0x41},
	"IntlBackslash": {86, 0x56, false, 0x0A},
	"F11":           {87, 0x57, false, 0x67}, "F12": {88, 0x58, false, 0x6F},
	"IntlRo":      {89, 0x73, false, 0x5E},
	"NumpadEnter": {96, 0x1C, true, 0x4C}, "ControlRight": {97, 0x1D, true, 0x3E},
	"NumpadDivide": {98, 0x35, true, 0x4B}, "PrintScreen": {99, 0x37, true, 0x69},
	"AltRight": {100, 0x38, true, 0x3D},
	"Home":     {102, 0x47, true, 0x73}, "ArrowUp": {103, 0x48, true, 0x7E}, "PageUp": {104, 0x49, true, 0x74},
	"ArrowLeft": {105, 0x4B, true, 0x7B}, "ArrowRight": {106, 0x4D, true, 0x7C},
	"End": {107, 0x4F, true, 0x77}, "ArrowDown": {108, 0x50, true, 0x7D}, "PageDown": {109, 0x51, true, 0x79},
	"Insert": {110, 0x52, true, 0x72}, "Delete": {111, 0x53, true, 0x75},
	"NumpadEqual": {117, 0x59, false, 0x51},
	"Pause":       {119, 0x45, false, noMac}, // Windows: sent as VK_PAUSE
	"IntlYen":     {124, 0x7D, false, 0x5D},
	"MetaLeft":    {125, 0x5B, true, 0x37}, "MetaRight": {126, 0x5C, true, 0x36},
	"OSLeft": {125, 0x5B, true, 0x37}, "OSRight": {126, 0x5C, true, 0x36},
	"ContextMenu": {127, 0x5D, true, 0x6E},
	"F13":         {183, 0x64, false, 0x69}, "F14": {184, 0x65, false, 0x6B}, "F15": {185, 0x66, false, 0x71},
	"F16": {186, 0x67, false, 0x6A}, "F17": {187, 0x68, false, 0x40}, "F18": {188, 0x69, false, 0x4F},
	"F19": {189, 0x6A, false, 0x50}, "F20": {190, 0x6B, false, 0x5A},
	"AudioVolumeMute": {113, 0x20, true, 0x4A}, "AudioVolumeDown": {114, 0x2E, true, 0x49},
	"AudioVolumeUp": {115, 0x30, true, 0x48},
}

// Button numbers follow MouseEvent.button: 0 left, 1 middle, 2 right,
// 3 back, 4 forward.
const (
	ButtonLeft   = 0
	ButtonMiddle = 1
	ButtonRight  = 2
)

// Injector injects input in display coordinates (see capture.Capturer.Bounds).
type Injector interface {
	Move(x, y int) error
	Button(b int, down bool) error
	// Scroll by wheel notches; positive dy scrolls down, positive dx right.
	Scroll(dx, dy int) error
	// Key presses or releases a key by KeyboardEvent.code.
	Key(code string, down bool) error
	Close()
}
