package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func syntheticTokenSecret() string {
	prefix := "Bearer" + " "
	token := "sk-" + "test-" + "token-" + "abcdef" + "1234567890"
	return prefix + token
}

func syntheticPEMSecret() string {
	begin := "-----" + "BEGIN " + "PRIVATE KEY" + "-----"
	body := " synthetic." + "example." + "com "
	end := "-----" + "END " + "PRIVATE KEY" + "-----"
	return begin + body + end
}

func syntheticURLSecret() string {
	scheme := "https" + "://"
	cred := "user" + ":" + "pass"
	host := "host." + "example." + "com"
	return scheme + cred + "@" + host + "/path"
}

func syntheticPlainSecret() string {
	return "secret" + "=" + "plain-" + "secret-" + "value"
}

func main() {
	printEnv := flag.Bool("print-env", false, "print the environment")
	pwd := flag.Bool("pwd", false, "print the working directory")
	args := flag.Bool("args", false, "print arguments after this flag")
	sleepMS := flag.Int("sleep-ms", 0, "sleep for milliseconds")
	exitCode := flag.Int("exit", 0, "exit code")
	writeBytes := flag.Int("write-bytes", 0, "write bytes to stdout")
	writeStderrBytes := flag.Int("write-stderr-bytes", 0, "write bytes to stderr")
	stderr := flag.Bool("stderr", false, "write output to stderr")
	spawn := flag.Bool("spawn-descendant", false, "spawn a sleeping descendant")
	child := flag.Bool("child", false, "run as a descendant")
	marker := flag.String("marker", "", "write the descendant PID here")
	printSecrets := flag.Bool("print-secrets", false, "print synthetic secret-shaped output")
	flag.Parse()
	if *child {
		if *marker != "" {
			if err := os.WriteFile(*marker, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				panic(err)
			}
		}
		time.Sleep(time.Duration(*sleepMS) * time.Millisecond)
		return
	}
	if *spawn {
		command := exec.Command(os.Args[0], "--child", "--marker", *marker, "--sleep-ms", strconv.Itoa(*sleepMS))
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			panic(err)
		}
	}
	if *printEnv {
		for _, entry := range os.Environ() {
			fmt.Println(entry)
		}
	}
	if *pwd {
		value, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		fmt.Println(value)
	}
	if *args {
		for _, arg := range flag.Args() {
			fmt.Println(arg)
		}
	}
	if *writeBytes > 0 {
		value := strings.Repeat("x", *writeBytes)
		if *stderr {
			_, _ = fmt.Fprint(os.Stderr, value)
		} else {
			_, _ = fmt.Fprint(os.Stdout, value)
		}
	}
	if *writeStderrBytes > 0 {
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("y", *writeStderrBytes))
	}
	if *printSecrets {
		_, _ = fmt.Fprint(os.Stdout, syntheticTokenSecret()+"\n")
		_, _ = fmt.Fprint(os.Stdout, syntheticPEMSecret()+"\n")
		_, _ = fmt.Fprint(os.Stderr, syntheticURLSecret()+"\n")
		_, _ = fmt.Fprint(os.Stderr, syntheticPlainSecret()+"\n")
	}
	if *sleepMS > 0 {
		time.Sleep(time.Duration(*sleepMS) * time.Millisecond)
	}
	os.Exit(*exitCode)
}
