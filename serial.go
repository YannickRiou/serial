package serial

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"time"
)

// End of line character (AKA EOL), newline character (ASCII 10, CR, '\n'). is used by default.
const EOL_DEFAULT byte = '\n'

/*******************************************************************************************
*******************************   TYPE DEFINITIONS 	****************************************
*******************************************************************************************/

type SerialPort struct {
	port          io.ReadWriteCloser
	name          string
	baud          int
	eol           uint8
	rxChar        chan byte
	closeReqChann chan bool
	closeAckChann chan error
	buff          *bytes.Buffer
	portIsOpen    bool
	// openPort      func(port string, baud int) (io.ReadWriteCloser, error)
}

/*******************************************************************************************
********************************   BASIC FUNCTIONS  ****************************************
*******************************************************************************************/

func New() *SerialPort {
	// Create new file
	return &SerialPort{
		eol:     EOL_DEFAULT,
		buff:    bytes.NewBuffer(make([]uint8, 256)),
	}
}

func (sp *SerialPort) Open(name string, baud int, timeout ...time.Duration) error {
	// Check if port is open
	if sp.portIsOpen {
		return fmt.Errorf("\"%s\" is already open", name)
	}
	var readTimeout time.Duration
	if len(timeout) > 0 {
		readTimeout = timeout[0]
	}
	// Open serial port
	comPort, err := openPort(name, baud, readTimeout)
	if err != nil {
		return fmt.Errorf("Unable to open port \"%s\" - %s", name, err)
	}
	// Open port succesfull
	sp.name = name
	sp.baud = baud
	sp.port = comPort
	sp.portIsOpen = true
	sp.buff.Reset()
	// Open channels
	sp.rxChar = make(chan byte)
	// Enable threads
	go sp.readSerialPort()
	go sp.processSerialPort()
	return nil
}

// This method close the current Serial Port.
func (sp *SerialPort) Close() error {
	if sp.portIsOpen {
		sp.portIsOpen = false
		close(sp.rxChar)
		return sp.port.Close()
	}
	return nil
}

// This method prints data trough the serial port.
func (sp *SerialPort) Write(data []byte) (n int, err error) {
	if sp.portIsOpen {
		n, err = sp.port.Write(data)
	} else {
		err = fmt.Errorf("Serial port is not open")
	}
	return
}

// This method prints data trough the serial port.
func (sp *SerialPort) Print(str string) error {
	if sp.portIsOpen {
		_, err := sp.port.Write([]byte(str))
	} else {
		return fmt.Errorf("Serial port is not open")
	}
	return nil
}

// Prints data to the serial port as human-readable ASCII text followed by a carriage return character
// (ASCII 13, CR, '\r') and a newline character (ASCII 10, LF, '\n').
func (sp *SerialPort) Println(str string) error {
	return sp.Print(str + "\r\n")
}

// Printf formats according to a format specifier and print data trough the serial port.
func (sp *SerialPort) Printf(format string, args ...interface{}) error {
	str := format
	if len(args) > 0 {
		str = fmt.Sprintf(format, args...)
	}
	return sp.Print(str)
}

//This method send a binary file trough the serial port. If EnableLog is active then this method will log file related data.
func (sp *SerialPort) SendFile(filepath string) error {
	// Aux Vars
	sentBytes := 0
	q := 512
	data := []byte{}
	// Read file
	file, err := ioutil.ReadFile(filepath)
	if err != nil {
		return err
	} else {
		fileSize := len(file)
		for sentBytes <= fileSize {
			//Try sending slices of less or equal than 512 bytes at time
			if len(file[sentBytes:]) > q {
				data = file[sentBytes:(sentBytes + q)]
			} else {
				data = file[sentBytes:]
			}
			// Write binaries
			_, err := sp.port.Write(data)
			if err != nil {
				return err
			} else {
				sentBytes += q
				time.Sleep(time.Millisecond * 100)
			}
		}
	}
	//Encode data to send
	return nil
}

// Read the first byte of the serial buffer.
func (sp *SerialPort) Read() (byte, error) {
	if sp.portIsOpen {
		return sp.buff.ReadByte()
	} else {
		return 0x00, fmt.Errorf("Serial port is not open")
	}
	return 0x00, nil
}

// Read first available line from serial port buffer.
//
// Line is delimited by the EOL character, newline character (ASCII 10, LF, '\n') is used by default.
//
// The text returned from ReadLine does not include the line end ("\r\n" or '\n').
func (sp *SerialPort) ReadLine() (string, error) {
	if sp.portIsOpen {
		line, err := sp.buff.ReadString(sp.eol)
		if err != nil {
			return "", err
		} else {
			return removeEOL(line), nil
		}
	} else {
		return "", fmt.Errorf("Serial port is not open")
	}
	return "", nil
}

// Wait for a defined regular expression for a defined amount of time.
func (sp *SerialPort) WaitForRegexTimeout(exp string, timeout time.Duration) (string, error) {

	if sp.portIsOpen {
		//Decode received data
		timeExpired := false

		regExpPatttern := regexp.MustCompile(exp)

		//Timeout structure
		c1 := make(chan string, 1)
		go func() {
			result := []string{}
			for !timeExpired {
				line, err := sp.ReadLine()
				if err != nil {
					// Do nothing
				} else {
					result = regExpPatttern.FindAllString(line, -1)
					if len(result) > 0 {
						c1 <- result[0]
						break
					}
				}
			}
		}()
		select {
		case data := <-c1:
			return data, nil
		case <-time.After(timeout):
			timeExpired = true
			return "", fmt.Errorf("Timeout expired")
		}
	} else {
		return "", fmt.Errorf("Serial port is not open")
	}
	return "", nil
}

// Available return the total number of available unread bytes on the serial buffer.
func (sp *SerialPort) Available() int {
	return sp.buff.Len()
}

// Change end of line character (AKA EOL), newline character (ASCII 10, LF, '\n') is used by default.
func (sp *SerialPort) EOL(c byte) {
	sp.eol = c
}

/*******************************************************************************************
******************************   PRIVATE FUNCTIONS  ****************************************
*******************************************************************************************/

func (sp *SerialPort) readSerialPort() {
	rxBuff := make([]byte, 256)
	for sp.portIsOpen {
		n, _ := sp.port.Read(rxBuff)
		// Write data to serial buffer
		sp.buff.Write(rxBuff[:n])
		for _, b := range rxBuff[:n] {
			if sp.portIsOpen {
				sp.rxChar <- b
			}
		}
	}
}

func (sp *SerialPort) processSerialPort() {
	screenBuff := make([]byte, 0)
	var lastRxByte byte
	for {
		if sp.portIsOpen {
			lastRxByte = <-sp.rxChar
			// Print received lines
			switch lastRxByte {
			case sp.eol:
				// EOL - Print received data
				screenBuff = make([]byte, 0) //Clean buffer
				break
			default:
				screenBuff = append(screenBuff, lastRxByte)
			}
		} else {
			break
		}
	}
}

func removeEOL(line string) string {
	var data []byte
	// Remove CR byte "\r"
	for _, b := range []byte(line) {
		switch b {
		case '\r':
			// Do nothing
		case '\n':
			// Do nothing
		default:
			data = append(data, b)
		}
	}
	return string(data)
}

// Converts the timeout values for Linux / POSIX systems
func posixTimeoutValues(readTimeout time.Duration) (vmin uint8, vtime uint8) {
	const MAXUINT8 = 1<<8 - 1 // 255
	// set blocking / non-blocking read
	var minBytesToRead uint8 = 1
	var readTimeoutInDeci int64
	if readTimeout > 0 {
		// EOF on zero read
		minBytesToRead = 0
		// convert timeout to deciseconds as expected by VTIME
		readTimeoutInDeci = (readTimeout.Nanoseconds() / 1e6 / 100)
		// capping the timeout
		if readTimeoutInDeci < 1 {
			// min possible timeout 1 Deciseconds (0.1s)
			readTimeoutInDeci = 1
		} else if readTimeoutInDeci > MAXUINT8 {
			// max possible timeout is 255 deciseconds (25.5s)
			readTimeoutInDeci = MAXUINT8
		}
	}
	return minBytesToRead, uint8(readTimeoutInDeci)
}
