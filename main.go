package main

import (
	// System
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"time"

	// PostgreSQL connection
	"github.com/jackc/pgx/v4/pgxpool"

	// Logging
	log "github.com/sirupsen/logrus"
)

// Type definitions

type Mover struct {
	Id       int
	Heading  int
	Velocity float64
	X        float64
	Y        float64
	Color    string
	Name     string
}

type Rectangle struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

type MoverProps struct {
	MaxHeadingChange  int
	MaxVelocityChange float64
	StartVelocity     float64
	StartRectangle    Rectangle
	SleepInterval     time.Duration
	MaxMovers         int
}

type MoverContext struct {
	DbPool *pgxpool.Pool
	Mutex  *sync.Mutex
	Props  MoverProps
}

var colorList = []string{
	"aqua", "fuchsia", "lime", "maroon", "red",
	"orange", "yellow", "green", "blue", "indigo", "violet",
	"navy", "purple", "teal", "greenyellow", "darkred", "cyan",
	"darkcyan", "darkorange", "lightpink", "salmon", "slategray",
}

// Globals
var moverProps MoverProps = MoverProps{
	MaxMovers:         100,
	MaxHeadingChange:  5,
	MaxVelocityChange: 0.1,
	StartVelocity:     2.0,
	SleepInterval:     time.Second,
	StartRectangle: Rectangle{
		MinX: -180,
		MinY: -70,
		MaxX: 180,
		MaxY: 70,
	},
}

func makeMover(moverId int) (Mover, error) {
	props := moverProps
	colorNum := moverId % len(colorList)
	xSize := props.StartRectangle.MaxX - props.StartRectangle.MinX
	ySize := props.StartRectangle.MaxY - props.StartRectangle.MinY
	startX := props.StartRectangle.MinX + float64(rand.Intn(int(xSize)))
	startY := props.StartRectangle.MinY + float64(rand.Intn(int(ySize)))
	startHeading := rand.Intn(360)

	mover := Mover{
		Id:       moverId,
		Heading:  startHeading,
		Velocity: props.StartVelocity,
		X:        startX,
		Y:        startY,
		Color:    colorList[colorNum],
		Name:     fmt.Sprintf("Object %d", moverId),
	}
	return mover, nil
}

func (m *Mover) Create(dbPool *pgxpool.Pool) error {
	sql := "INSERT INTO moving.objects (id, geog, color) VALUES ($1, ST_MakePoint($2, $3)::geography, $4)"
	_, err := dbPool.Exec(context.Background(), sql, m.Id, m.X, m.Y, m.Color)
	return err
}

func (m *Mover) Move(dbPool *pgxpool.Pool) error {
	headingChange := rand.Intn(2*moverProps.MaxHeadingChange) - moverProps.MaxHeadingChange
	m.Heading = (m.Heading + headingChange) % 360
	radianHeading := math.Pi * float64(m.Heading+90.0) / 180.0
	m.X = m.X + math.Cos(radianHeading)*m.Velocity
	m.Y = m.Y + math.Sin(radianHeading)*m.Velocity
	if m.X > moverProps.StartRectangle.MaxX {
		m.X = moverProps.StartRectangle.MinX + (m.X - moverProps.StartRectangle.MaxX)
	}
	if m.Y > moverProps.StartRectangle.MaxY {
		m.Y = moverProps.StartRectangle.MinY + (m.Y - moverProps.StartRectangle.MaxY)
	}
	if m.X < moverProps.StartRectangle.MinX {
		m.X = moverProps.StartRectangle.MaxX - (moverProps.StartRectangle.MinX - m.X)
	}
	if m.Y < moverProps.StartRectangle.MinY {
		m.Y = moverProps.StartRectangle.MaxY - (moverProps.StartRectangle.MinY - m.Y)
	}
	velocityChange := rand.NormFloat64() * moverProps.MaxVelocityChange
	m.Velocity = m.Velocity + velocityChange

	sql := "UPDATE moving.objects SET geog = ST_MakePoint($1, $2)::geography, ts = Now() WHERE id = $3"
	_, err := dbPool.Exec(context.Background(), sql, m.X, m.Y, m.Id)
	if err != nil {
		log.Fatal(err)
	}
	return err
}

func (m Mover) Print() {
	fmt.Printf("Mover %d\n", m.Id)
	fmt.Printf("  Color: %s\n", m.Color)
	fmt.Printf("  X: %f\n", m.X)
	fmt.Printf("  Y: %f\n", m.Y)
	fmt.Printf("  Heading: %d\n", m.Heading)
	fmt.Printf("  Velocity: %f\n", m.Velocity)
	fmt.Printf("\n")
}

func moverRoutine(ctx context.Context, moverId int) {
	moverCtx := ctx.Value("moverContext").(MoverContext)
	dbPool := moverCtx.DbPool
	mover, _ := makeMover(moverId)
	mover.Create(dbPool)

	log.Infof("In moverRoutine with Mover %d", moverId)

	for t := true; t; {
		err := mover.Move(dbPool)
		if err != nil {
			return
		}
		mover.Print()
		d := (moverProps.SleepInterval / 2) + time.Duration(rand.Intn(int(moverProps.SleepInterval)))
		time.Sleep(d)
	}

}

func main() {

	// Initialize random number generator
	rand.Seed(time.Now().UnixNano())

	// Read environment configuration first
	var dbUrl string
	if dbUrl = os.Getenv("DATABASE_URL"); dbUrl != "" {
		// viper.Set("DbConnection", dbURL)
		log.Info("Found environment variable DATABASE_URL")
	} else {
		log.Fatal("Unable to find DATABASE_URL")
	}

	var dbPool *pgxpool.Pool
	var dbConfig *pgxpool.Config
	var err error

	dbConfig, err = pgxpool.ParseConfig(dbUrl)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	dbPool, err = pgxpool.ConnectConfig(ctx, dbConfig)
	if err != nil {
		log.Fatal(err)
	}

	moverContext := MoverContext{
		DbPool: dbPool,
		Mutex:  &sync.Mutex{},
		Props:  moverProps,
	}
	ctxValue := context.WithValue(
		context.Background(),
		"moverContext", moverContext)
	ctxCancel, cancel := context.WithCancel(ctxValue)

	for i := 0; i < moverProps.MaxMovers; i++ {
		go moverRoutine(ctxCancel, i)
	}

	// Wait here for interrupt signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	// Shut down everything attached to this context before exit
	cancel()
	// time.Sleep(100 * time.Millisecond)

	// relay := broadcast.NewRelay[msg]() // Create a relay for msg values
	// defer relay.Close()

	// // Listener goroutines
	// for i := 0; i < 2; i++ {
	//     go func(i int) {
	//         l := relay.Listener(1)  // Create a listener with a buffer capacity of 1
	//         for n := range l.Ch() { // Ranges over notifications
	//             fmt.Printf("listener %d has received a notification: %v\n", i, n)
	//         }
	//     }(i)
	// }

	// // Notifiers
	// time.Sleep(time.Second)
	// relay.Notify(msgA)                                     // Send notification with guaranteed delivery
	// // ctx, _ := context.WithTimeout(context.Background(), 10) // Context with immediate timeout
	// // relay.NotifyCtx(ctx, msgB)                             // Send notification respecting context cancellation
	// relay.Notify(msgB)
	// time.Sleep(time.Second)                                // Allow time for previous messages to be processed
	// relay.Broadcast(msgC)                                  // Send notification without guaranteed delivery
	// time.Sleep(time.Second)                                // Allow time for previous messages to be processed
}
