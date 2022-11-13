package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type JsonMessage struct {
	Message string `json:"message"`
}

type OweNote struct {
	FromUser string `json:"from"`
	ToUser   string `json:"to"`
	Amount   int    `json:"amount"`
}

type BankAccount struct {
	UserName string `json:"username"`
	Balance  int    `json:"balance"`
}

type BalanceInfo struct {
	Account BankAccount `json:"account"`
	OweNote []OweNote   `json:"owenote"`
}

func createNewAccount(userName string) BankAccount {
	return BankAccount{userName, 0}
}

func sendJSONError(ctx *gin.Context, message string) {
	ctx.JSON(http.StatusBadRequest, JsonMessage{Message: message})
}

func getAllAccountHandler(collection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		ctx.JSON(0, createNewAccount("Alice"))
	}
}

func getAccountHandler(collection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		ctx.JSON(0, JsonMessage{Message: fmt.Sprintf("name: %s", collection.Name())})
	}
}

func createAccountHandler(collection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		var newAccount BankAccount
		if err := ctx.BindJSON(&newAccount); err != nil {
			ctx.JSON(http.StatusBadRequest, JsonMessage{Message: err.Error()})
			return
		}
		if result, _ := collection.Find(ctx, newAccount); result.RemainingBatchLength() > 0 {
			ctx.JSON(http.StatusBadRequest, JsonMessage{Message: "User already exist."})
			return
		}
		if _, err := collection.InsertOne(ctx, newAccount); err != nil {
			ctx.JSON(http.StatusBadRequest, JsonMessage{Message: err.Error()})
			return
		}
		ctx.JSON(http.StatusCreated, newAccount)
	}
}

func getAccountBalanceHandler(bankCollection, oweNoteCollection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		var accountInput BankAccount
		if err := ctx.BindJSON(&accountInput); err != nil {
			sendJSONError(ctx, fmt.Sprintf("Failed to read input data: %s.", err.Error()))
			return
		}
		accountSearchCursor, err := bankCollection.Find(context.TODO(),
			bson.D{{Key: "username", Value: accountInput.UserName}},
		)
		if err != nil {
			sendJSONError(ctx, fmt.Sprintf("Failed to search user: %s.", err.Error()))
			return
		}
		if accountSearchCursor.RemainingBatchLength() <= 0 {
			sendJSONError(ctx, "User not found.")
			return
		}
		balanceInfo := make([]BalanceInfo, 0)
		for accountSearchCursor.Next(context.TODO()) {
			var bankAccount BankAccount
			err := accountSearchCursor.Decode(&bankAccount)
			if err != nil {
				sendJSONError(ctx, err.Error())
				return
			}

			oweNoteSearchCursor, err := oweNoteCollection.Find(context.TODO(),
				bson.D{{Key: "from", Value: bankAccount.UserName}},
			)
			if err != nil {
				sendJSONError(ctx, err.Error())
				return
			}

			oweNotes := make([]OweNote, oweNoteSearchCursor.RemainingBatchLength())
			oweNoteSearchCursor.All(context.TODO(), &oweNotes)

			balanceInfo = append(balanceInfo, BalanceInfo{Account: bankAccount, OweNote: oweNotes})
		}
		ctx.JSON(http.StatusOK, balanceInfo)
	}
}

func main() {
	clientOptions := options.Client().ApplyURI("mongodb://localhost:27017")

	// Connect to MongoDB
	client, err := mongo.Connect(context.TODO(), clientOptions)

	if err != nil {
		log.Fatal(err)
	}

	// Check the connection
	err = client.Ping(context.TODO(), nil)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Connected to MongoDB!")

	goDatabase := client.Database("goDatabase")
	bankCollection := goDatabase.Collection("BankAccount")
	oweNoteCollection := goDatabase.Collection("OweNote")

	router := gin.Default()

	router.GET("/account", getAccountHandler(bankCollection))
	router.GET("/account/balance", getAccountBalanceHandler(bankCollection, oweNoteCollection))
	router.GET("/account/all", getAllAccountHandler(bankCollection))
	router.POST("/account/create", createAccountHandler(bankCollection))

	router.Run("localhost:8080")

	err = client.Disconnect(context.TODO())

	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Connection to MongoDB closed.")
}
