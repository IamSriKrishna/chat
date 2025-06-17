package main

import (
	"context"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/websocket/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type User struct {
	ID       primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	Username string             `json:"username" bson:"username"`
	IsOnline bool               `json:"is_online" bson:"is_online"`
}

type Message struct {
	ID          primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	From        string             `json:"from" bson:"from"`
	To          string             `json:"to" bson:"to"`
	Content     string             `json:"content" bson:"content"`
	Timestamp   time.Time          `json:"timestamp" bson:"timestamp"`
	MessageType string             `json:"message_type" bson:"message_type"`
}

type WebSocketMessage struct {
	Type    string      `json:"type"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	Content string      `json:"content"`
	Data    interface{} `json:"data,omitempty"`
}

type Client struct {
	Username string
	Conn     *websocket.Conn
}

var (
	clients   = make(map[string]*Client)
	db        *mongo.Database
	usersColl *mongo.Collection
	msgsColl  *mongo.Collection
)

func main() {
	log.Println("Starting Chat Server...")

	// Connect to MongoDB
	log.Println("Connecting to MongoDB...")
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI("mongodb+srv://srik090704:srik090704@cluster0.nsjmbgv.mongodb.net/?retryWrites=true&w=majority&appName=Cluster0"))
	if err != nil {
		log.Fatal("Failed to connect to MongoDB:", err)
	}
	defer client.Disconnect(context.TODO())

	// Test MongoDB connection
	err = client.Ping(context.TODO(), nil)
	if err != nil {
		log.Fatal("MongoDB connection test failed:", err)
	}
	log.Println("✓ MongoDB connected successfully")

	db = client.Database("chatapp")
	usersColl = db.Collection("users")
	msgsColl = db.Collection("messages")

	// Create indexes
	log.Println("Creating database indexes...")
	usersColl.Indexes().CreateOne(context.TODO(), mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	log.Println("✓ Database indexes created")

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			log.Printf("Error: %v", err)
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		},
	})

	// Add request logging
	app.Use(logger.New(logger.Config{
		Format: "[${time}] ${status} - ${method} ${path} - ${latency}\n",
	}))

	// CORS middleware
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
	}))

	// WebSocket upgrade middleware
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	// Add a health check endpoint
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":    "ok",
			"timestamp": time.Now(),
			"message":   "Chat server is running",
		})
	})

	// Routes
	app.Post("/api/register", registerUser)
	app.Get("/api/users", getUsers)
	app.Get("/api/messages/:from/:to", getMessageHistory)
	app.Get("/ws/:username", websocket.New(handleWebSocket))

	log.Println("✓ Routes configured")
	log.Println("🚀 Server starting on :4000")
	log.Println("📡 Health check: http://localhost:4000/health")
	log.Println("👥 Users API: http://localhost:4000/api/users")
	log.Println("📝 Register API: http://localhost:4000/api/register")
	log.Fatal(app.Listen("0.0.0.0:4000"))

}

func registerUser(c *fiber.Ctx) error {
	log.Printf("📝 Registration request from %s", c.IP())

	var user User
	if err := c.BodyParser(&user); err != nil {
		log.Printf("❌ Invalid request body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	log.Printf("📝 Attempting to register user: %s", user.Username)

	user.IsOnline = false
	user.ID = primitive.NewObjectID()

	_, err := usersColl.InsertOne(context.TODO(), user)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			log.Printf("⚠️ Username already exists: %s", user.Username)
			return c.Status(409).JSON(fiber.Map{"error": "Username already exists"})
		}
		log.Printf("❌ Failed to create user: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create user"})
	}

	log.Printf("✅ User registered successfully: %s", user.Username)
	return c.JSON(user)
}

func getUsers(c *fiber.Ctx) error {
	log.Printf("👥 Fetching users list from %s", c.IP())

	cursor, err := usersColl.Find(context.TODO(), bson.M{})
	if err != nil {
		log.Printf("❌ Failed to fetch users: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch users"})
	}
	defer cursor.Close(context.TODO())

	var users []User
	if err = cursor.All(context.TODO(), &users); err != nil {
		log.Printf("❌ Failed to decode users: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to decode users"})
	}

	log.Printf("✅ Returning %d users", len(users))
	return c.JSON(users)
}

func getMessageHistory(c *fiber.Ctx) error {
	from := c.Params("from")
	to := c.Params("to")

	log.Printf("💬 Fetching message history: %s <-> %s", from, to)

	filter := bson.M{
		"$or": []bson.M{
			{"from": from, "to": to},
			{"from": to, "to": from},
		},
	}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}})
	cursor, err := msgsColl.Find(context.TODO(), filter, opts)
	if err != nil {
		log.Printf("❌ Failed to fetch messages: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch messages"})
	}
	defer cursor.Close(context.TODO())

	var messages []Message
	if err = cursor.All(context.TODO(), &messages); err != nil {
		log.Printf("❌ Failed to decode messages: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to decode messages"})
	}

	log.Printf("✅ Returning %d messages", len(messages))
	return c.JSON(messages)
}

func handleWebSocket(c *websocket.Conn) {
	username := c.Params("username")
	log.Printf("🔌 WebSocket connection: %s", username)

	client := &Client{
		Username: username,
		Conn:     c,
	}
	clients[username] = client

	// Update user online status
	usersColl.UpdateOne(
		context.TODO(),
		bson.M{"username": username},
		bson.M{"$set": bson.M{"is_online": true}},
	)

	defer func() {
		log.Printf("🔌 WebSocket disconnection: %s", username)
		delete(clients, username)
		usersColl.UpdateOne(
			context.TODO(),
			bson.M{"username": username},
			bson.M{"$set": bson.M{"is_online": false}},
		)
		c.Close()
	}()

	// Send online users list
	sendOnlineUsers(client)

	// Broadcast user joined
	broadcastUserStatus(username, true)

	for {
		var wsMsg WebSocketMessage
		if err := c.ReadJSON(&wsMsg); err != nil {
			log.Printf("❌ WebSocket read error for %s: %v", username, err)
			break
		}

		switch wsMsg.Type {
		case "message":
			handleMessage(wsMsg)
		case "typing":
			handleTyping(wsMsg)
		}
	}

	// Broadcast user left
	broadcastUserStatus(username, false)
}

func handleMessage(wsMsg WebSocketMessage) {
	log.Printf("💬 Message: %s -> %s", wsMsg.From, wsMsg.To)

	// Save message to database
	message := Message{
		ID:          primitive.NewObjectID(),
		From:        wsMsg.From,
		To:          wsMsg.To,
		Content:     wsMsg.Content,
		Timestamp:   time.Now(),
		MessageType: "text",
	}

	_, err := msgsColl.InsertOne(context.TODO(), message)
	if err != nil {
		log.Printf("❌ Failed to save message: %v", err)
		return
	}

	// Send message to recipient if online
	if recipient, ok := clients[wsMsg.To]; ok {
		response := WebSocketMessage{
			Type:    "message",
			From:    wsMsg.From,
			To:      wsMsg.To,
			Content: wsMsg.Content,
			Data: map[string]interface{}{
				"id":        message.ID.Hex(),
				"timestamp": message.Timestamp,
			},
		}
		recipient.Conn.WriteJSON(response)
		log.Printf("✅ Message delivered to %s", wsMsg.To)
	} else {
		log.Printf("⚠️ Recipient %s is offline", wsMsg.To)
	}
}

func handleTyping(wsMsg WebSocketMessage) {
	if recipient, ok := clients[wsMsg.To]; ok {
		response := WebSocketMessage{
			Type: "typing",
			From: wsMsg.From,
			To:   wsMsg.To,
		}
		recipient.Conn.WriteJSON(response)
	}
}

func sendOnlineUsers(client *Client) {
	var onlineUsers []string
	for username := range clients {
		if username != client.Username {
			onlineUsers = append(onlineUsers, username)
		}
	}

	response := WebSocketMessage{
		Type: "online_users",
		Data: onlineUsers,
	}
	client.Conn.WriteJSON(response)
}

func broadcastUserStatus(username string, isOnline bool) {
	response := WebSocketMessage{
		Type: "user_status",
		Data: map[string]interface{}{
			"username":  username,
			"is_online": isOnline,
		},
	}

	for _, client := range clients {
		if client.Username != username {
			client.Conn.WriteJSON(response)
		}
	}
}
