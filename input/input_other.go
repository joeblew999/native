//go:build !darwin

// Fallback for platforms without an input backend yet. Keeps the module
// building for every GOOS; every call fails with ErrUnsupported.

package input

func trusted() bool { return false }

func (a *App) click(_, _ int, _ Button) error { return ErrUnsupported }

func (a *App) scroll(_, _ int) error { return ErrUnsupported }

func (a *App) keyTap(_ Key, _ []Modifier) error { return ErrUnsupported }

func (a *App) typeString(_ string) error { return ErrUnsupported }

func moveMouse(_, _ int) error { return ErrUnsupported }

func mousePosition() (int, int, error) { return 0, 0, ErrUnsupported }

func click(_ Button) error { return ErrUnsupported }

func mouseToggle(_ Button, _ bool) error { return ErrUnsupported }

func scroll(_, _ int) error { return ErrUnsupported }

func keyTap(_ Key, _ []Modifier) error { return ErrUnsupported }

func typeString(_ string) error { return ErrUnsupported }
