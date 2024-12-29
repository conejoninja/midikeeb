package main

import (
	_ "embed"
	"image/color"
	"machine"
	"machine/usb"
	"machine/usb/adc/midi"
	"math/rand/v2"
	"strconv"
	"time"

	pio "github.com/tinygo-org/pio/rp2-pio"
	"github.com/tinygo-org/pio/rp2-pio/piolib"

	"tinygo.org/x/drivers"
	"tinygo.org/x/drivers/encoders"
	"tinygo.org/x/drivers/ssd1306"
	"tinygo.org/x/tinyfont"
	"tinygo.org/x/tinyfont/freemono"
	"tinygo.org/x/tinyfont/proggy"
)

const (
	white  = 0x3F3F3FFF
	red    = 0x00FF00FF
	green  = 0xFF0000FF
	blue   = 0x0000FFFF
	yellow = 0x88FF00FF
	purple = 0x800080FF
	pink   = 0x662280FF
	black  = 0x000000FF

	LAYERS  = 3
	ROWS    = 3
	COLUMNS = 4

	MELODYLAYER    = 2
	MELODYCHANNEL  = 1
	DRUMKITCHANNEL = 10
)

var (
	invertRotaryPins = false
	currentLayer     = 0
	displayFrame     = 0

	textWhite = color.RGBA{255, 255, 255, 255}
	textBlack = color.RGBA{0, 0, 0, 255}

	rotaryOldValue, rotaryNewValue int
	rotaryPressed                  bool

	colPins = []machine.Pin{
		machine.GPIO5,
		machine.GPIO6,
		machine.GPIO7,
		machine.GPIO8,
	}

	rowPins = []machine.Pin{
		machine.GPIO9,
		machine.GPIO10,
		machine.GPIO11,
	}

	matrixBtn         [COLUMNS][ROWS]bool
	matrixDebounceBtn [COLUMNS][ROWS]bool
	colors            []uint32
	frame             int16

	keys [LAYERS][COLUMNS][ROWS]bool

	layer, row, column uint8

	lastTick, currentTick int64

	notes       = []int{60, 62, 64, 36, 50, 55, 36, 50, 55}
	notesMelody = []int{53, 60, 67, 55, 62, 69, 57, 64, 71, 59, 65, 72}
	btnColors   = []int64{red, green, blue, yellow, purple, pink}

	sleepBPM = 425 // 141bpm ~= 425ms sleep
	bpm      = "141"
)

type WS2812B struct {
	Pin machine.Pin
	ws  *piolib.WS2812B
}

func NewWS2812B(pin machine.Pin) *WS2812B {
	s, _ := pio.PIO0.ClaimStateMachine()
	ws, _ := piolib.NewWS2812B(s, pin)
	ws.EnableDMA(true)
	return &WS2812B{
		ws: ws,
	}
}

func (ws *WS2812B) WriteRaw(rawGRB []uint32) error {
	return ws.ws.WriteRaw(rawGRB)
}

func main() {
	usb.Product = "MIDI KEEB"

	time.Sleep(1 * time.Second)
	i2c := machine.I2C0
	i2c.Configure(machine.I2CConfig{
		Frequency: 2.8 * machine.MHz,
		SDA:       machine.GPIO12,
		SCL:       machine.GPIO13,
	})

	display := ssd1306.NewI2C(i2c)
	display.Configure(ssd1306.Config{
		Address:  0x3C,
		Width:    128,
		Height:   64,
		Rotation: drivers.Rotation180,
	})
	display.ClearDisplay()

	enc := encoders.NewQuadratureViaInterrupt(
		machine.GPIO4,
		machine.GPIO3,
	)

	enc.Configure(encoders.QuadratureConfig{
		Precision: 4,
	})
	rotaryBtn := machine.GPIO2
	rotaryBtn.Configure(machine.PinConfig{Mode: machine.PinInputPullup})

	for _, c := range colPins {
		c.Configure(machine.PinConfig{Mode: machine.PinOutput})
		c.Low()
	}

	for _, c := range rowPins {
		c.Configure(machine.PinConfig{Mode: machine.PinInputPulldown})
	}

	colors = []uint32{
		black, black, black, black,
		black, black, black, black,
		black, black, black, black,
	}
	ws := NewWS2812B(machine.GPIO1)

	for {
		display.ClearBuffer()
		if !rotaryBtn.Get() {
			if !rotaryPressed {
				layer++
				if layer >= LAYERS {
					layer = 0
				}
				colors = []uint32{
					black, black, black, black,
					black, black, black, black,
					black, black, black, black,
				}
				for j := 0; j < ROWS; j++ {
					for i := 0; i < COLUMNS; i++ {
						if keys[layer][i][j] {
							colors[i*ROWS+j] = uint32(btnColors[layer*3+uint8(j)])
						}
					}
				}
			}
			rotaryPressed = true
		} else {
			rotaryPressed = false
		}
		getMatrixState()

		if rotaryNewValue = enc.Position(); rotaryNewValue != rotaryOldValue {
			if rotaryNewValue < rotaryOldValue {
				sleepBPM += 10
				if sleepBPM > 1000 {
					sleepBPM = 1000
				}
				bpm = strconv.Itoa(60000 / sleepBPM)
			} else {
				sleepBPM -= 10
				if sleepBPM < 100 {
					sleepBPM = 100
				}
				bpm = strconv.Itoa(60000 / sleepBPM)
			}
			rotaryOldValue = rotaryNewValue
		}

		for j := 0; j < ROWS; j++ {
			for i := 0; i < COLUMNS; i++ {
				if layer == MELODYLAYER { // MELODY
					c := colors[i*ROWS+j]
					g := ((c & 0xFF000000) >> 1) & 0xFF000000
					r := ((c & 0x00FF0000) >> 1) & 0x00FF0000
					b := ((c & 0x0000FF00) >> 1) & 0x0000FF00
					c = g | r | b | 0xFF
					colors[i*ROWS+j] = c
					keys[MELODYLAYER][i][j] = false
					if matrixBtn[i][j] {
						if !matrixDebounceBtn[i][j] {
							colors[i*ROWS+j] = rand.Uint32()
							matrixDebounceBtn[i][j] = true
							keys[MELODYLAYER][i][j] = true
						}
					} else {
						matrixDebounceBtn[i][j] = false
					}
				} else {
					if matrixBtn[i][j] {
						if !matrixDebounceBtn[i][j] {
							matrixDebounceBtn[i][j] = true
							if keys[layer][i][j] {
								colors[i*ROWS+j] = black
							} else {
								colors[i*ROWS+j] = uint32(btnColors[layer*3+uint8(j)])
							}
							keys[layer][i][j] = !keys[layer][i][j]
						}
					} else {
						matrixDebounceBtn[i][j] = false
					}
				}
			}
		}

		currentTick = time.Now().UnixMilli()
		if currentTick-lastTick >= int64(sleepBPM) {
			frame++
			if frame >= 4 {
				frame = 0
			}
			lastTick = currentTick

			for l := uint8(0); l < LAYERS-1; l++ {
				for r := uint8(0); r < ROWS; r++ {
					if keys[l][frame][r] {
						midi.Midi.NoteOn(0, DRUMKITCHANNEL, midi.Note(notes[3*l+r]), 64)
					}
				}
			}
		}
		for j := 0; j < ROWS; j++ {
			for i := 0; i < COLUMNS; i++ {
				if keys[MELODYLAYER][i][j] {
					midi.Midi.NoteOff(0, MELODYCHANNEL, midi.Note(notesMelody[3*i+j]), 64)
					midi.Midi.NoteOn(0, MELODYCHANNEL, midi.Note(notesMelody[3*i+j]), 64)
				}
			}
		}

		display.FillRectangle(32*frame, 0, 32, 8, textWhite)
		if layer == 0 {
			tinyfont.WriteLine(&display, &freemono.Bold12pt7b, 4, 32, "CONGA", textWhite)
		} else if layer == 1 {
			tinyfont.WriteLine(&display, &freemono.Bold12pt7b, 4, 32, "DRUMKIT", textWhite)
		} else {
			tinyfont.WriteLine(&display, &freemono.Bold12pt7b, 4, 32, "MELODY", textWhite)
		}

		tw, _ := tinyfont.LineWidth(&proggy.TinySZ8pt7b, bpm+" BPM")
		tinyfont.WriteLine(&display, &proggy.TinySZ8pt7b, 124-int16(tw), 60, bpm+" BPM", textWhite)
		ws.WriteRaw(colors)
		display.Display()
		time.Sleep(10 * time.Millisecond)
	}

}

func getMatrixState() {
	colPins[0].High()
	colPins[1].Low()
	colPins[2].Low()
	colPins[3].Low()
	time.Sleep(1 * time.Millisecond)

	matrixBtn[0][0] = rowPins[0].Get()
	matrixBtn[0][1] = rowPins[1].Get()
	matrixBtn[0][2] = rowPins[2].Get()

	// COL2
	colPins[0].Low()
	colPins[1].High()
	colPins[2].Low()
	colPins[3].Low()
	time.Sleep(1 * time.Millisecond)

	matrixBtn[1][0] = rowPins[0].Get()
	matrixBtn[1][1] = rowPins[1].Get()
	matrixBtn[1][2] = rowPins[2].Get()

	// COL3
	colPins[0].Low()
	colPins[1].Low()
	colPins[2].High()
	colPins[3].Low()
	time.Sleep(1 * time.Millisecond)

	matrixBtn[2][0] = rowPins[0].Get()
	matrixBtn[2][1] = rowPins[1].Get()
	matrixBtn[2][2] = rowPins[2].Get()

	// COL4
	colPins[0].Low()
	colPins[1].Low()
	colPins[2].Low()
	colPins[3].High()
	time.Sleep(1 * time.Millisecond)

	matrixBtn[3][0] = rowPins[0].Get()
	matrixBtn[3][1] = rowPins[1].Get()
	matrixBtn[3][2] = rowPins[2].Get()
}
