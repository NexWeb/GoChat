package main

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"github.com/monnand/dhkx"
)

// struct for holding room info
type Room struct {
	name        string
	description string
	password    string
	maxClients  int
	welcomeMsg  string
}

// struct for holding client info
type Client struct {
	connection net.Conn
	nick       string
	clientKey  string
}

var rooms []Room
var clientRoom map[Client]Room
var publicKeyCode string
var nickCode string

// clear the terminal screen
func clear() {
	switch runtime.GOOS {
	case "linux":
		cmd := exec.Command("clear")
		cmd.Stdout = os.Stdout
		cmd.Run()
	case "windows":
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		cmd.Run()
	default:
		fmt.Println("Attempted to clear terminal, but OS is not supported.")
	}
}

// load rooms from rooms.txt, return []Room
func loadRooms() []Room {
	var roomList []Room

	// read data from rooms file
	dat, err := ioutil.ReadFile("rooms.txt")
	if err != nil {
		panic(err)
	}

	// split rooms
	roomsString := strings.Split(string(dat), "|")

	// make a new room from each string in roomsString
	for _, e := range roomsString[0 : len(roomsString)-1] {
		roomString := strings.Split(e, "-")
		r := Room{name: roomString[0], welcomeMsg: roomString[1], description: roomString[2]}
		roomList = append(roomList, r)
	}

	return roomList
}

// saves a new room to rooms.txt
func saveRoom(name string, welcomeMsg string, description string) {
	// read data from rooms file
	dat, err := ioutil.ReadFile("rooms.txt")
	if err != nil {
		panic(err)
	}

	// string thats being added to the file
	newRoomString := name + "-" + welcomeMsg + "-" + description + "|"
	// new file content
	datString := string(dat) + newRoomString

	// write the updated content to file
	err2 := ioutil.WriteFile("rooms.txt", []byte(datString), 0644)
	if err2 != nil {
		panic(err2)
	}
}

// init variables
func setup() {
	publicKeyCode = "ssd990=+?¡][ªs)(sdª]ßð=S)]"
	nickCode = "!#28jKas>zzx'**!+?,>lzc012"
	clientRoom = make(map[Client]Room)
	rooms = loadRooms()

	// if no rooms where loaded (aka no rooms exist), create a lobby
	if len(rooms) < 1 {
		saveRoom("Lobby", "Welcome to Lobby", "This is the lobby")
		rooms = loadRooms()
	}
}

// check if []byte contains specific byte
func contains(s []byte, e byte) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// exchange key with client
func exchangeKeys(c Client) {

	var clientPublicKey []byte

	// generate private key
	g, _ := dhkx.GetGroup(0)
	serverPrivateKey, _ := g.GeneratePrivateKey(nil)

	// make sure the key does not contain '\n' or '%'
	for {
		if contains(serverPrivateKey.Bytes(), byte('\n')) || contains(serverPrivateKey.Bytes(), byte('%')) {
			newKey, _ := g.GeneratePrivateKey(nil)
			serverPrivateKey = newKey
		} else {
			break
		}
	}

	// generate public key
	serverPublicKey := serverPrivateKey.Bytes()

	// listening for client public key
	for {
		message, _ := bufio.NewReader(c.connection).ReadString('\n')
		if len(message) > len(publicKeyCode) {
			if message[0:len(publicKeyCode)] == publicKeyCode {
				clientPublicKey = []byte(message[len(publicKeyCode) : len(message)-1])
				break
			}
		}
	}

	// sending server public key
	msg := publicKeyCode + string(serverPublicKey) + "\n"
	fmt.Fprintf(c.connection, msg)

	// finding common key
	pubKey := dhkx.NewPublicKey(clientPublicKey)
	k, _ := g.ComputeKey(pubKey, serverPrivateKey)
	c.clientKey = string(k.Bytes()[0:32])

	var nick string

	// waiting for nickname
	for {
		msg, _ := bufio.NewReader(c.connection).ReadString('\n')
		message := decrypt([]byte(c.clientKey), msg)
		if len(message) > len(nickCode) {
			if message[0:len(nickCode)] == nickCode {
				nick = message[len(nickCode):len(message)]
				break
			}
		}
	}

	// start the client thread and place in room 0 (Lobby)
	c.nick = nick
	c.startThread()
	fmt.Println(c.connection.RemoteAddr().String() + " connected as " + c.nick)
	switchRoom(c, rooms[0])
}


func main() {
	setup()
	startServer()
}

// start server listening
func startServer() {
	clear()

	port := ":8081"
	ln, _ := net.Listen("tcp", port)
	//clear()
	fmt.Println("Server is listening on " + port)

	// accept incomming connections, make new client struct and start thread for exchanging keys
	for {
		conn, _ := ln.Accept()
		newClient := Client{connection: conn}
		go exchangeKeys(newClient)
	}
}

// listen for messages from client
func (c Client) listener() {
	quitting := false

	for {
		// get message
		message, _ := bufio.NewReader(c.connection).ReadString('\n')

		// check length
		if len(message) > 0 {
			msg := message[0 : len(message)-1]

			// decrypt message
			msgDecrypted := decrypt([]byte(c.clientKey), msg)

			// if client wants to quit
			if msgDecrypted == "!quit" {
				quitting = true
			}

			// if not quitting, print message, check for commands
			if !quitting {
				fmt.Print(c.connection.RemoteAddr().String() + " (" + c.nick + "): " + msgDecrypted)

				if !checkForCmd(c, msgDecrypted) {
					// for each client in the same room, encrypt the message
					// with their key and send
					for mapKey, value := range clientRoom {
						if mapKey != c && value == clientRoom[c] {
							mapKey.sendEncrypted(makeBold(c.nick) + " > " + msgDecrypted)
						}
					}
				}
			} else {
				// delete client from the map, close connection and print
				for m, v := range clientRoom {
					if m != c && v == clientRoom[c] {
						m.sendEncrypted(makeBold(c.nick) + " disconnected\n")
					}
				}
				delete(clientRoom, c)
				c.connection.Close()
				fmt.Println(c.connection.RemoteAddr().String() + " has disconnected")
				break
			}
		}
	}
}

// send message to client
func (c *Client) send(message []byte) {
	c.connection.Write(message)
}

// send encrypted message to client
func (c *Client) sendEncrypted(message string) {
	cryptMsg := encrypt([]byte(c.clientKey), message)
	c.connection.Write([]byte(cryptMsg + "\n"))
}

// start a listener thread for a client
func (c *Client) startThread() {
	go c.listener()
}

// check messsage for commands
func checkForCmd(client Client, msg string) bool {
	if len(msg) > 1 {
		words := strings.Split(msg[0:len(msg)-1], " ")
		switch words[0] {
		case "!help":
			message := makeBold("!help") + " - This help menu.\n" + makeBold("!room") + " - List all available rooms\n" + makeBold("!room [roomname]") + " - Change room to a specified room.\n"
			client.sendEncrypted(message)
		// !room displays a list of available rooms
		case "!room":
			// if more than 1 word = we have arguments
			if len(words) > 1 {
				// if attempting to join the room client is already in
				if words[1] == clientRoom[client].name {
					message := "You are already in this room\nType" + makeBold(" !room ") + "to get a list of other available chatrooms\n"
					client.sendEncrypted(message)
				} else {
					// look for the room the client wants to join, and join if found
					for _, element := range rooms {
						if element.name == words[1] {
							client.sendEncrypted("Switching room: " + makeBold(element.name) + "\n")
							switchRoom(client, element)
						}
					}
				}
			} else {
				roomMsg := ""
				for _, element := range rooms {
					roomMsg += " - " + element.name + "\n"
				}
				client.sendEncrypted(roomMsg)
			}
			return true
		}
	}
	return false
}

// client switch room
func switchRoom(client Client, room Room) {
	// tell clients in the room that we are leaving that
	// and clients in the new room that we are joining
	for k, v := range clientRoom {
		if k != client && v == room {
			k.sendEncrypted(makeBold(client.nick) + " has joined " + makeBold(room.name) + "\n")
		}
		if k != client && v != room {
			k.sendEncrypted(makeBold(client.nick) + " has left " + makeBold(clientRoom[client].name) + "\n")
		}
	}

	// change room
	clientRoom[client] = room
	// send the rooms welcome message
	client.sendEncrypted(makeBold(room.welcomeMsg) + "\n")
}

// make a new room
func makeRoom(name string, welcomeMsg string) {
	newRoom := Room{name: name, welcomeMsg: welcomeMsg}
	rooms = append(rooms, newRoom)
}

// make string bold
func makeBold(text string) string {
	return "\033[1m" + text + "\033[0m"
}

// create key
func createKey() []byte {
	randKey := make([]byte, 32)
	_, err := rand.Read(randKey)
	if err != nil {
		panic(err)
	}
	return randKey
}

// encrypt message
func encrypt(key []byte, text string) string {
	// []byte of text to encrypt
	plaintext := []byte(text)

	// get cipher from key
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}

	// generating initialization vector
	ciphertext := make([]byte, aes.BlockSize+len(plaintext))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		panic(err)
	}

	// encrypting
	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintext)

	// return encrypted text
	return base64.URLEncoding.EncodeToString(ciphertext)
}

// decrypt message
func decrypt(key []byte, cryptoText string) string {
	// get ciphertext
	ciphertext, _ := base64.URLEncoding.DecodeString(cryptoText)

	// get cipher from key
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}

	// check length
	if len(ciphertext) < aes.BlockSize {
		panic("Ciphertext too short")
	}

	// generate initialization vector
	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	// decrypting
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertext, ciphertext)

	// return decrypted text
	return fmt.Sprintf("%s", ciphertext)
}
