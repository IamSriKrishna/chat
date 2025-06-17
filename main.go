package main

import (
	"context"
	"log"
	"sort"
	"strings"
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
	RoomID      string             `json:"room_id" bson:"room_id"`
	From        string             `json:"from" bson:"from"`
	To          string             `json:"to" bson:"to"`
	Content     string             `json:"content" bson:"content"`
	Timestamp   time.Time          `json:"timestamp" bson:"timestamp"`
	MessageType string             `json:"message_type" bson:"message_type"`
	IsDelivered bool               `json:"is_delivered" bson:"is_delivered"`
	IsRead      bool               `json:"is_read" bson:"is_read"`
}

type ChatRoom struct {
	ID           primitive.ObjectID `json:"id" bson:"_id,omitempty"`
	RoomID       string             `json:"room_id" bson:"room_id"`
	Participants []string           `json:"participants" bson:"participants"`
	CreatedAt    time.Time          `json:"created_at" bson:"created_at"`
	LastActivity time.Time          `json:"last_activity" bson:"last_activity"`
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
	roomsColl *mongo.Collection
)

// Generate a consistent room ID for two users
func generateRoomID(user1, user2 string) string {
	users := []string{user1, user2}
	sort.Strings(users) // Ensure consistent ordering
	return strings.Join(users, "_")
}

// Get or create a chat room for two users
func getOrCreateRoom(user1, user2 string) (string, error) {
	roomID := generateRoomID(user1, user2)
	
	// Check if room exists
	var room ChatRoom
	err := roomsColl.FindOne(context.TODO(), bson.M{"room_id": roomID}).Decode(&room)
	
	if err == mongo.ErrNoDocuments {
		// Create new room
		room = ChatRoom{
			ID:           primitive.NewObjectID(),
			RoomID:       roomID,
			Participants: []string{user1, user2},
			CreatedAt:    time.Now(),
			LastActivity: time.Now(),
		}
		
		_, err = roomsColl.InsertOne(context.TODO(), room)
		if err != nil {
			return "", err
		}
		log.Printf("✅ Created new chat room: %s", roomID)
	} else if err != nil {
		return "", err
	}
	
	return roomID, nil
}

// Update room's last activity
func updateRoomActivity(roomID string) {
	roomsColl.UpdateOne(
		context.TODO(),
		bson.M{"room_id": roomID},
		bson.M{"$set": bson.M{"last_activity": time.Now()}},
	)
}

func main() {
	log.Println("Starting Enhanced Chat Server...")

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
	roomsColl = db.Collection("rooms")

	// Create indexes
	log.Println("Creating database indexes...")
	
	// User indexes
	usersColl.Indexes().CreateOne(context.TODO(), mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	
	// Message indexes for better query performance
	msgsColl.Indexes().CreateOne(context.TODO(), mongo.IndexModel{
		Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "timestamp", Value: 1}},
	})
	msgsColl.Indexes().CreateOne(context.TODO(), mongo.IndexModel{
		Keys: bson.D{{Key: "from", Value: 1}, {Key: "to", Value: 1}},
	})
	
	// Room indexes
	roomsColl.Indexes().CreateOne(context.TODO(), mongo.IndexModel{
		Keys:    bson.D{{Key: "room_id", Value: 1}},
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
			"message":   "Enhanced Chat server is running",
		})
	})

	// Routes
	app.Post("/api/register", registerUser)
	app.Get("/api/users", getUsers)
	app.Get("/api/messages/:from/:to", getMessageHistory)
	app.Get("/api/rooms/:username", getUserRooms)
	app.Post("/api/messages/mark-read", markMessagesAsRead)
	app.Get("/ws/:username", websocket.New(handleWebSocket))

	log.Println("✓ Routes configured")
	log.Println("🚀 Server starting on :4000")
	log.Println("📡 Health check: http://localhost:4000/health")
	log.Println("👥 Users API: http://localhost:4000/api/users")
	log.Println("📝 Register API: http://localhost:4000/api/register")
	log.Fatal(app.Listen("0.0.0.0:4000"))
}

func registerUser(c *fiber.Ctx) error {
	log.Printf("📝 Registration/Login request from %s", c.IP())

	var user User
	if err := c.BodyParser(&user); err != nil {
		log.Printf("❌ Invalid request body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Validate username
	if strings.TrimSpace(user.Username) == "" {
		log.Printf("❌ Empty username provided")
		return c.Status(400).JSON(fiber.Map{"error": "Username cannot be empty"})
	}

	// Normalize username (trim spaces and convert to lowercase for consistency)
	user.Username = strings.TrimSpace(strings.ToLower(user.Username))
	log.Printf("📝 Processing user: %s", user.Username)

	// First, check if user already exists
	var existingUser User
	err := usersColl.FindOne(context.TODO(), bson.M{"username": user.Username}).Decode(&existingUser)

	if err == nil {
		// User already exists - this is essentially a "login"
		log.Printf("✅ User already exists, logging in: %s", user.Username)
		
		// Update the user's online status if needed
		usersColl.UpdateOne(
			context.TODO(),
			bson.M{"username": user.Username},
			bson.M{"$set": bson.M{"is_online": true}},
		)
		
		return c.JSON(fiber.Map{
			"user": existingUser,
			"message": "Welcome back!",
			"action": "login",
		})
	} else if err != mongo.ErrNoDocuments {
		// Some other database error occurred
		log.Printf("❌ Database error while checking user: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Database error"})
	}

	// User doesn't exist, create new user
	log.Printf("📝 Creating new user: %s", user.Username)
	user.IsOnline = true // Set as online since they're registering/logging in
	user.ID = primitive.NewObjectID()

	_, err = usersColl.InsertOne(context.TODO(), user)
	if err != nil {
		// Handle the rare case where user was created between our check and insert
		if mongo.IsDuplicateKeyError(err) {
			// Try to fetch the user that was just created
			err = usersColl.FindOne(context.TODO(), bson.M{"username": user.Username}).Decode(&existingUser)
			if err == nil {
				log.Printf("✅ User was created concurrently, returning existing data: %s", user.Username)
				return c.JSON(fiber.Map{
					"user": existingUser,
					"message": "Welcome back!",
					"action": "login",
				})
			}
		}
		log.Printf("❌ Failed to create user: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create user"})
	}

	log.Printf("✅ User registered successfully: %s", user.Username)
	return c.JSON(fiber.Map{
		"user": user,
		"message": "Welcome! Your account has been created.",
		"action": "register",
	})
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

	// Get or create room for these users
	roomID, err := getOrCreateRoom(from, to)
	if err != nil {
		log.Printf("❌ Failed to get/create room: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to access chat room"})
	}

	// Query messages by room_id for better performance and consistency
	filter := bson.M{"room_id": roomID}
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

	log.Printf("✅ Returning %d messages from room %s", len(messages), roomID)
	return c.JSON(messages)
}

func getUserRooms(c *fiber.Ctx) error {
	username := c.Params("username")
	log.Printf("🏠 Fetching rooms for user: %s", username)

	// Find all rooms where user is a participant
	filter := bson.M{"participants": username}
	cursor, err := roomsColl.Find(context.TODO(), filter)
	if err != nil {
		log.Printf("❌ Failed to fetch rooms: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch rooms"})
	}
	defer cursor.Close(context.TODO())

	var rooms []ChatRoom
	if err = cursor.All(context.TODO(), &rooms); err != nil {
		log.Printf("❌ Failed to decode rooms: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to decode rooms"})
	}

	log.Printf("✅ Returning %d rooms for user %s", len(rooms), username)
	return c.JSON(rooms)
}

func markMessagesAsRead(c *fiber.Ctx) error {
	var req struct {
		Username string `json:"username"`
		RoomID   string `json:"room_id"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Mark all messages in the room as read for this user
	filter := bson.M{
		"room_id": req.RoomID,
		"to":      req.Username,
		"is_read": false,
	}
	
	update := bson.M{"$set": bson.M{"is_read": true}}
	
	result, err := msgsColl.UpdateMany(context.TODO(), filter, update)
	if err != nil {
		log.Printf("❌ Failed to mark messages as read: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "Failed to mark messages as read"})
	}

	log.Printf("✅ Marked %d messages as read for %s in room %s", result.ModifiedCount, req.Username, req.RoomID)
	return c.JSON(fiber.Map{"marked_count": result.ModifiedCount})
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
		case "join_room":
			handleJoinRoom(wsMsg, client)
		}
	}

	// Broadcast user left
	broadcastUserStatus(username, false)
}

func handleMessage(wsMsg WebSocketMessage) {
	log.Printf("💬 Message: %s -> %s", wsMsg.From, wsMsg.To)

	// Get or create room for these users
	roomID, err := getOrCreateRoom(wsMsg.From, wsMsg.To)
	if err != nil {
		log.Printf("❌ Failed to get/create room: %v", err)
		return
	}

	// Save message to database with room information
	message := Message{
		ID:          primitive.NewObjectID(),
		RoomID:      roomID,
		From:        wsMsg.From,
		To:          wsMsg.To,
		Content:     wsMsg.Content,
		Timestamp:   time.Now(),
		MessageType: "text",
		IsDelivered: false,
		IsRead:      false,
	}

	_, err = msgsColl.InsertOne(context.TODO(), message)
	if err != nil {
		log.Printf("❌ Failed to save message: %v", err)
		return
	}

	// Update room activity
	updateRoomActivity(roomID)

	// Prepare response with room information
	response := WebSocketMessage{
		Type:    "message",
		From:    wsMsg.From,
		To:      wsMsg.To,
		Content: wsMsg.Content,
		Data: map[string]interface{}{
			"id":        message.ID.Hex(),
			"room_id":   roomID,
			"timestamp": message.Timestamp,
		},
	}

	// Send message to recipient if online
	if recipient, ok := clients[wsMsg.To]; ok {
		recipient.Conn.WriteJSON(response)
		
		// Mark as delivered
		msgsColl.UpdateOne(
			context.TODO(),
			bson.M{"_id": message.ID},
			bson.M{"$set": bson.M{"is_delivered": true}},
		)
		
		log.Printf("✅ Message delivered to %s in room %s", wsMsg.To, roomID)
	} else {
		log.Printf("⚠️ Recipient %s is offline, message saved to room %s", wsMsg.To, roomID)
	}

	// Also send confirmation back to sender
	if sender, ok := clients[wsMsg.From]; ok {
		response.Data.(map[string]interface{})["status"] = "sent"
		sender.Conn.WriteJSON(response)
	}
}

func handleJoinRoom(wsMsg WebSocketMessage, client *Client) {
	if roomData, ok := wsMsg.Data.(map[string]interface{}); ok {
		if otherUser, ok := roomData["other_user"].(string); ok {
			roomID, err := getOrCreateRoom(client.Username, otherUser)
			if err != nil {
				log.Printf("❌ Failed to join room: %v", err)
				return
			}
			
			response := WebSocketMessage{
				Type: "room_joined",
				Data: map[string]interface{}{
					"room_id":    roomID,
					"other_user": otherUser,
				},
			}
			client.Conn.WriteJSON(response)
			log.Printf("✅ User %s joined room %s", client.Username, roomID)
		}
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