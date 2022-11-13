package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type JsonMessage struct {
	Message string `json:"message"`
}

func isUsernameValid(userName string) bool {
	return regexp.MustCompile(`^[\w]+$`).MatchString(userName)
}

type ErrInvalidUsername struct {
	UserName string
}

func (err *ErrInvalidUsername) Error() string {
	return fmt.Sprintf("ErrUsername: username \"%s\" is invalid.", err.UserName)
}

type ErrSameSourceAndTarget struct{}

func (err *ErrSameSourceAndTarget) Error() string {
	return "ErrSameSourceAndTarget: source and target account cannot be the same."
}

type ErrInputRead struct {
	InputError error
}

func (err *ErrInputRead) Error() string {
	return fmt.Sprintf("Failed to read input: %v.", err.InputError)
}

type ErrLessThanEqualZero struct {
	Name string
}

func (err *ErrLessThanEqualZero) Error() string {
	return fmt.Sprintf("ErrLessThanEqualZero: Value \"%s\" must be greater than zero.", err.Name)
}

type TransferNote struct {
	FromUser string `json:"fromuser"`
	ToUser   string `json:"touser"`
	Amount   int    `json:"amount"`
}

func (note *TransferNote) Error() error {
	if note.Amount <= 0 {
		return &ErrLessThanEqualZero{Name: "Amount"}
	}
	if !isUsernameValid(note.FromUser) {
		return &ErrInvalidUsername{UserName: note.FromUser}
	}
	if !isUsernameValid(note.ToUser) {
		return &ErrInvalidUsername{UserName: note.ToUser}
	}
	if note.FromUser == note.ToUser {
		return &ErrSameSourceAndTarget{}
	}
	return nil
}

type BankAccount struct {
	UserName string `json:"username"`
	Balance  int    `json:"balance"`
	Debt     int    `json:"debt"`
}

type ErrUserAlreadyExist struct {
	Account BankAccount
}

func (err *ErrUserAlreadyExist) Error() string {
	return fmt.Sprintf("ErrUserAlreadyExist: user \"%s\" already exist.", err.Account.UserName)
}

func (account *BankAccount) Error() error {
	if !isUsernameValid(account.UserName) {
		return &ErrInvalidUsername{UserName: account.UserName}
	}
	return nil
}

type TransactionInput struct {
	UserName string `json:"username"`
	Amount   int    `json:"amount"`
}

func (deposit *TransactionInput) Error() error {
	if !isUsernameValid(deposit.UserName) {
		return &ErrInvalidUsername{UserName: deposit.UserName}
	}
	if deposit.Amount <= 0 {
		return &ErrLessThanEqualZero{Name: "amount"}
	}
	return nil
}

func min(firstValue, secondValue int) int {
	if firstValue < secondValue {
		return firstValue
	}
	return secondValue
}

func sendErrorJSON(ctx *gin.Context, message string) {
	ctx.JSON(http.StatusBadRequest, JsonMessage{Message: message})
}

func createErrorMessage(errorType, message string) string {
	return fmt.Sprintf("%v: %v.", errorType, message)
}

func sendError(ctx *gin.Context, err error) {
	sendErrorJSON(ctx, err.Error())
}

func sendErrUserNotFound(ctx *gin.Context, err error, userName string) bool {
	if err == mongo.ErrNoDocuments {
		sendErrorJSON(ctx, createErrorMessage("ErrNoDocuments",
			fmt.Sprintf("User %s not found", userName),
		))
		return true
	}
	return false
}

func getAllAccountHandler(accountCollection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		accountSearchResult, err := accountCollection.Find(context.TODO(), bson.D{})
		if err != nil {
			sendError(ctx, &ErrInputRead{InputError: err})
			return
		}
		accountList := make([]BankAccount, accountSearchResult.RemainingBatchLength())
		accountSearchResult.All(context.TODO(), &accountList)
		ctx.JSON(http.StatusOK, accountList)
	}
}

func createAccountHandler(accountCollection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		var newAccount BankAccount
		if err := ctx.BindJSON(&newAccount); err != nil {
			sendError(ctx, &ErrInputRead{InputError: err})
			return
		}

		if err := newAccount.Error(); err != nil {
			sendError(ctx, err)
			return
		}

		newAccount.Balance = 0

		if err := accountCollection.FindOne(context.TODO(), bson.D{{
			Key: "username", Value: newAccount.UserName,
		}}).Err(); err == nil {
			sendError(ctx, &ErrUserAlreadyExist{Account: newAccount})
			return
		}

		if _, err := accountCollection.InsertOne(ctx, newAccount); err != nil {
			sendError(ctx, err)
			return
		}

		ctx.JSON(http.StatusCreated, newAccount)
	}
}

func getAccountHandler(accountCollection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		var accountInput BankAccount
		if err := ctx.BindJSON(&accountInput); err != nil {
			sendError(ctx, &ErrInputRead{InputError: err})
			return
		}

		var accountSearch BankAccount
		if err := accountCollection.FindOne(context.TODO(), bson.D{{
			Key: "username", Value: accountInput.UserName,
		}}).Decode(&accountSearch); err != nil {
			if sendErrUserNotFound(ctx, err, accountInput.UserName) {
				return
			}
			sendError(ctx, err)
			return
		}

		ctx.JSON(http.StatusOK, accountSearch)
	}
}

func depositToAccountHandler(accountCollection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		var depositInput TransactionInput
		if err := ctx.BindJSON(&depositInput); err != nil {
			sendError(ctx, &ErrInputRead{InputError: err})
			return
		}

		if err := depositInput.Error(); err != nil {
			sendError(ctx, err)
			return
		}

		var targetAccount BankAccount
		searchFilter := bson.D{{Key: "username", Value: depositInput.UserName}}
		if err := accountCollection.FindOne(
			context.TODO(), searchFilter,
		).Decode(&targetAccount); err != nil {
			if sendErrUserNotFound(ctx, err, depositInput.UserName) {
				return
			}
			sendError(ctx, err)
			return
		}

		if targetAccount.Debt > 0 {
			payedAmount := min(targetAccount.Debt, depositInput.Amount)
			targetAccount.Debt -= payedAmount
			depositInput.Amount -= payedAmount
		}

		targetAccount.Balance += depositInput.Amount
		accountCollection.ReplaceOne(context.TODO(), searchFilter, targetAccount)

		ctx.JSON(http.StatusOK, targetAccount)
	}
}

func withdrawFromAccountHandler(accountCollection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		var withdrawInput TransactionInput
		if err := ctx.BindJSON(&withdrawInput); err != nil {
			sendError(ctx, &ErrInputRead{InputError: err})
			return
		}

		if err := withdrawInput.Error(); err != nil {
			sendError(ctx, err)
			return
		}

		var targetAccount BankAccount
		searchFilter := bson.D{{Key: "username", Value: withdrawInput.UserName}}
		if err := accountCollection.FindOne(
			context.TODO(), searchFilter,
		).Decode(&targetAccount); err != nil {
			if sendErrUserNotFound(ctx, err, withdrawInput.UserName) {
				return
			}
			sendError(ctx, err)
			return
		}

		withdrawnAmount := min(targetAccount.Balance, withdrawInput.Amount)
		targetAccount.Balance -= withdrawnAmount
		targetAccount.Debt += withdrawInput.Amount - withdrawnAmount
		accountCollection.ReplaceOne(context.TODO(), searchFilter, targetAccount)

		ctx.JSON(http.StatusOK, targetAccount)
	}
}

func transferHandler(accountCollection *mongo.Collection) func(*gin.Context) {
	return func(ctx *gin.Context) {
		var transferNote TransferNote
		if err := ctx.BindJSON(&transferNote); err != nil {
			sendError(ctx, &ErrInputRead{InputError: err})
			return
		}

		if err := transferNote.Error(); err != nil {
			sendError(ctx, err)
			return
		}

		var sourceAccount BankAccount
		sourceFilter := bson.D{{Key: "username", Value: transferNote.FromUser}}
		if err := accountCollection.FindOne(
			context.TODO(), sourceFilter,
		).Decode(&sourceAccount); err != nil {
			if sendErrUserNotFound(ctx, err, transferNote.FromUser) {
				return
			}
			sendError(ctx, err)
			return
		}

		var targetAccount BankAccount
		targetFilter := bson.D{{Key: "username", Value: transferNote.ToUser}}
		if err := accountCollection.FindOne(
			context.TODO(), targetFilter,
		).Decode(&targetAccount); err != nil {
			if sendErrUserNotFound(ctx, err, transferNote.ToUser) {
				return
			}
			sendError(ctx, err)
			return
		}

		payedAmount := min(transferNote.Amount, targetAccount.Debt)
		targetAccount.Debt -= payedAmount
		targetAccount.Balance += transferNote.Amount - payedAmount
		accountCollection.ReplaceOne(context.TODO(), targetFilter, targetAccount)

		transferredAmount := min(transferNote.Amount, sourceAccount.Balance)
		sourceAccount.Balance -= transferredAmount
		sourceAccount.Debt += transferNote.Amount - transferredAmount
		accountCollection.ReplaceOne(context.TODO(), sourceFilter, sourceAccount)

		ctx.JSON(http.StatusOK, []BankAccount{sourceAccount, targetAccount})
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
	accountCollection := goDatabase.Collection("BankAccount")

	router := gin.Default()

	router.GET("/account", getAccountHandler(accountCollection))
	router.GET("/account/all", getAllAccountHandler(accountCollection))
	router.POST("/account/create", createAccountHandler(accountCollection))

	router.POST("/deposit", depositToAccountHandler(accountCollection))
	router.POST("/withdraw", withdrawFromAccountHandler(accountCollection))
	router.POST("/transfer", transferHandler(accountCollection))

	router.Run("localhost:8080")

	err = client.Disconnect(context.TODO())

	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Connection to MongoDB closed.")
}
