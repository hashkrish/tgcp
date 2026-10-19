package main

import (
	"fmt"
	"os/exec"
)

func main() {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", "test")
	cmd.Run()

	cmd2 := exec.Command("tmux", "split-window", "-h", "--", "echo", "hello; touch /tmp/pwned")
	err := cmd2.Run()
	if err != nil {
		fmt.Println("Error:", err)
	}

	cmd3 := exec.Command("ls", "-l", "/tmp/pwned")
	out, _ := cmd3.CombinedOutput()
	fmt.Println(string(out))

	cmd4 := exec.Command("tmux", "kill-session", "-t", "test")
	cmd4.Run()
}
