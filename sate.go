package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/akamensky/argparse"
	"gopkg.in/yaml.v3"
)

type State struct {
	Default string
	Initial string
	Get     string
	Set     string
	Onget   []string
	Onset   []string
}

type Config struct {
	Host   string
	Port   int
	States map[string]*State
}

type Client struct {
	watch string
	conn  net.Conn
}

var VERSION string

func main() {
	root := argparse.NewParser("sate", "Stupid state manager using tcp server")

	noDaemon := root.Flag("n", "nodaemon", &argparse.Options{
		Required: false,
		Help:     "Start without daemon mode",
	})

	configPathDefault, err := os.UserConfigDir()
	if err != nil {
		log.Fatal("ERROR: variable for home user not found")
	}
	configFile := root.File("c", "config", os.O_RDWR, 600, &argparse.Options{
		Required: false,
		Help:     "Path to config file",
		Default:  configPathDefault + "/sate/state.yml",
	})

	version := root.NewCommand("v", "Print version")

	start := root.NewCommand("start", "Start server")
	stop := root.NewCommand("stop", "Stop server")

	get := root.NewCommand("get", "Get state")
	getKey := get.StringPositional(&argparse.Options{
		Required: true,
		Help:     "key",
	})

	set := root.NewCommand("set", "Set state")
	setKey := set.StringPositional(&argparse.Options{
		Required: true,
		Help:     "key",
	})
	setValue := set.StringPositional(&argparse.Options{
		Required: true,
		Help:     "value",
	})

	watch := root.NewCommand("watch", "Watch state")
	watchKey := watch.StringPositional(&argparse.Options{
		Required: true,
		Help:     "key",
	})

	err = root.Parse(os.Args)
	if err != nil {
		fmt.Print(root.Usage(err))
		return
	}

	if version.Happened() {
		fmt.Println("Version:", VERSION)
		return
	}

	configBuffer, err := os.ReadFile(configFile.Name())
	if err != nil {
		log.Fatal("CONFIG FILE ERROR: ", err)
	}
	config := Config{}
	if err = yaml.Unmarshal(configBuffer, &config); err != nil {
		log.Fatal("CONFIG PARSE ERROR: ", err)
	}
	if config.Port == 0 {
		config.Port = 9123
	}
	if config.Host == "" {
		config.Host = "localhost"
	}

	switch {
	case start.Happened():
		if !*noDaemon {
			daemon := false
			for _, env := range os.Environ() {
				if daemon = env == "SATE_DAEMON=true"; daemon {
					break
				}
			}
			if !daemon {
				binary, _ := os.Executable()
				process, err := os.StartProcess(binary, os.Args, &os.ProcAttr{
					Env: append([]string{"SATE_DAEMON=true"}, os.Environ()...),
				})
				if err != nil {
					log.Print("DAEMON ERROR: ", err)
				}
				if err := process.Release(); err != nil {
					log.Print("DAEMON ERROR: ", err)
				}
				return
			}
		}

		stateInitial(&config)
		server(&config, config.Host, config.Port)

	case stop.Happened():
		client := client(config.Host, config.Port)
		defer client.Close()
		client.Write([]byte("stop"))

	case get.Happened():
		if *getKey == "" {
			fmt.Print(root.Usage(nil))
			return
		}

		client := client(config.Host, config.Port)
		defer client.Close()

		client.Write([]byte("get " + *getKey))

		buffer := make([]byte, 1024)
		length, _ := client.Read(buffer)
		fmt.Println(string(buffer[:length]))

	case set.Happened():
		if *setKey == "" || *setValue == "" {
			fmt.Print(root.Usage(nil))
			return
		}

		client := client(config.Host, config.Port)
		defer client.Close()

		client.Write([]byte("set " + *setKey + " " + *setValue))

		buffer := make([]byte, 1024)
		length, _ := client.Read(buffer)
		fmt.Println(string(buffer[:length]))

	case watch.Happened():
		if *watchKey == "" {
			fmt.Print(root.Usage(nil))
			return
		}

		client := client(config.Host, config.Port)
		defer client.Close()

		client.Write([]byte("watch " + *watchKey))

		var wg sync.WaitGroup
		wg.Add(1)
		go clientWatch(client, &wg)
		wg.Wait()

	default:
		fmt.Print(root.Usage(nil))
		return
	}
}

func removeLastNewLine(in []byte) []byte {
	if in[len(in)-1] == 10 {
		return in[:len(in)-1]
	}
	return in
}

func SaveStringf(format string, a string) string {
	if strings.Count(format, "%s") >= 1 {
		return fmt.Sprintf(format, a)
	}
	return format
}

func stateInitial(config *Config) {
	for _, state := range config.States {
		if state.Initial != "" {
			command := SaveStringf(state.Initial, state.Default)
			out, err := exec.Command("sh", "-c", command).Output()

			if err != nil {
				log.Print("INITIAL ERROR: ", command)
				log.Print("INITIAL ERROR: ", err)
			} else {
				state.Default = string(removeLastNewLine(out))
			}
		}
	}
}

func server(config *Config, host string, port int) {
	server, err := net.Listen("tcp", host+":"+strconv.Itoa(port))

	if err != nil {
		log.Fatal("SERVER ERROR: ", err)
	}

	defer server.Close()

	log.Printf("SERVER: %s:%d", host, port)

	clients := map[string]*Client{}

CLIENT_LOOP:
	for {
		conn, err := server.Accept()

		if err != nil {
			switch {
			case errors.Is(err, net.ErrClosed):
				log.Print("SERVER CLOSE")
				break CLIENT_LOOP

			default:
				log.Print("ACCEPT CLIENT ERROR: ", err)
				continue CLIENT_LOOP
			}
		}

		log.Print("CLIENT: connected")
		client := Client{watch: "", conn: conn}
		clients[conn.RemoteAddr().String()] = &client

		go serverHandleRequest(config, server, clients, &client)
	}
}

func serverHandleRequest(config *Config, server net.Listener, clients map[string]*Client, client *Client) {
	defer client.conn.Close()
	defer func() {
		log.Print("CLIENT: disconnect")
		_, found := clients[client.conn.RemoteAddr().String()]
		if found {
			delete(clients, client.conn.RemoteAddr().String())
		}
	}()

	buffer := make([]byte, 1024)
	data := bufio.NewReader(client.conn)

MESSAGE_LOOP:
	for {
		length, err := data.Read(buffer)
		message := strings.Split(string(buffer[:length]), " ")

		switch err {
		case io.EOF:
			break MESSAGE_LOOP

		case nil:
			log.Print("SERVER MESSAGE: ", message)
			switch message[0] {
			case "stop":
				server.Close()
				break MESSAGE_LOOP

			case "get":
				state, ok := config.States[message[1]]
				if ok {
					messageBack := []byte(state.Default)
					callback := true

					if state.Get != "" {
						command := SaveStringf(state.Get, state.Default)
						out, err := exec.Command("sh", "-c", command).Output()
						callback = err == nil

						if err != nil {
							log.Print("COMMAND GET ERROR: ", command)
							log.Print("COMMAND GET ERROR: ", err)
							messageBack = []byte("ERROR: " + command)
						} else {
							messageBack = removeLastNewLine(out)
						}
					}

					client.conn.Write(messageBack)

					if callback {
						for _, command := range state.Onget {
							if err := exec.Command("sh", "-c", SaveStringf(command, state.Default)).Run(); err != nil {
								log.Print("STATE ON GET ERROR: ", command)
								log.Print("STATE ON GET ERROR: ", err)
							}
						}
					}
				} else {
					client.conn.Write([]byte("nil"))
				}

			case "set":
				state, ok := config.States[message[1]]
				if ok {
					state.Default = message[2]
					messageBack := []byte(state.Default)
					callback := true

					if state.Set != "" {
						command := SaveStringf(state.Set, state.Default)
						out, err := exec.Command("sh", "-c", command).Output()
						callback = err == nil

						if err != nil {
							log.Print("COMMAND SET ERROR: ", command)
							log.Print("COMMAND SET ERROR: ", err)
							messageBack = []byte("ERROR: " + SaveStringf(state.Get, state.Default))
						} else {
							messageBack = removeLastNewLine(out)
							state.Default = string(messageBack)
						}
					}

					client.conn.Write(messageBack)

					for _, item := range clients {
						if item.watch == message[1] {
							item.conn.Write([]byte(state.Default))
						}
					}

					if callback {
						for _, command := range state.Onset {
							if err := exec.Command("sh", "-c", SaveStringf(command, state.Default)).Run(); err != nil {
								log.Print("STATE ON SET ERROR: ", command)
								log.Print("STATE ON SET ERROR: ", err)
							}
						}
					}
				} else {
					client.conn.Write([]byte("nil"))
				}

			case "watch":
				client.watch = message[1]
				state, ok := config.States[message[1]]
				if ok {
					client.conn.Write([]byte(state.Default))
				} else {
					client.conn.Write([]byte("nil"))
				}
			}
		default:
			log.Print("MESSAGE LOOP ERROR: ", err)
			break MESSAGE_LOOP
		}
	}
}

func client(host string, port int) net.Conn {
	addr := strings.Join([]string{host, strconv.Itoa(port)}, ":")
	client, err := net.Dial("tcp", addr)

	if err != nil {
		log.Fatal("CLIENT ERROR: ", err)
	}

	return client
}

func clientWatch(client net.Conn, wg *sync.WaitGroup) {
	defer wg.Done()

	buffer := make([]byte, 1024)
	data := bufio.NewReader(client)

MESSAGE_LOOP:
	for {
		length, err := data.Read(buffer)
		message := string(buffer[:length])

		switch err {
		case io.EOF:
			break MESSAGE_LOOP

		case nil:
			fmt.Println(message)

		default:
			break MESSAGE_LOOP
		}
	}
}
