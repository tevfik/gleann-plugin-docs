package tui

import (
	"fmt"
	"os"
)

// ServeArgs are the command-line arguments of the background plugin server.
func ServeArgs(port int) []string {
	return []string{"serve", "--port", fmt.Sprint(port)}
}

// serveMenuItem is the Serve menu entry with its live on/off state.
func serveMenuItem(port int) menuItem {
	if ServeEnabled() {
		return menuItem{title: "Serve (Disable)", desc: fmt.Sprintf("Plugin server is running on :%d and starts at login — select to stop & disable", port), icon: "⏹ ", screen: ScreenServe}
	}
	return menuItem{title: "Serve (Enable)", desc: fmt.Sprintf("Run the gleann plugin server (:%d) in the background, starting at login", port), icon: "🔌", screen: ScreenServe}
}

// runServeToggle enables or disables the background server, then waits for enter.
func runServeToggle(port int) {
	fmt.Println()
	if ServeEnabled() {
		fmt.Println("⏹  Stopping and disabling the plugin server...")
		if err := DisableServe(); err != nil {
			fmt.Printf("✗ Error: %v\n", err)
		} else {
			fmt.Println("✓ Plugin server stopped; it will no longer start at login.")
		}
	} else {
		fmt.Println("🔌 Enabling the plugin server in the background...")
		if err := EnableServe(ServeArgs(port)); err != nil {
			fmt.Printf("✗ Error: %v\n", err)
		} else {
			fmt.Printf("✓ Plugin server running on :%d and will start at login.\n", port)
			fmt.Println("  Linux logs: journalctl --user -u gleann-plugin-docs-serve -f")
		}
	}
	fmt.Println("\nPress enter to return to menu...")
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
}
