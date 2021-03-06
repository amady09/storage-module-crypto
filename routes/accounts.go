package routes

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/opacity/storage-node/models"
	"github.com/opacity/storage-node/services"
	"github.com/opacity/storage-node/utils"
)

const Unpaid = "unpaid"
const Pending = "pending"
const Paid = "paid"
const Expired = "expired"

type accountCreateObj struct {
	StorageLimit     int `json:"storageLimit" validate:"required,gte=10" minimum:"10" maximum:"2048" example:"100"`
	DurationInMonths int `json:"durationInMonths" validate:"required,gte=1" minimum:"1" example:"12"`
}

type accountCreateReq struct {
	verification
	requestBody
	accountCreateObj accountCreateObj
}

type accountCreateRes struct {
	ExpirationDate time.Time      `json:"expirationDate" validate:"required"`
	Invoice        models.Invoice `json:"invoice" validate:"omitempty"`
}

type accountDataRes struct {
	PaymentStatus string        `json:"paymentStatus" example:"paid"`
	Error         error         `json:"error" swaggertype:"string" example:"the error encountered while checking"`
	Account       accountGetObj `json:"account" validate:"required"`
	StripeData    stripeDataObj `json:"stripeData"`
}

type accountUnpaidRes struct {
	accountDataRes
	Invoice models.Invoice `json:"invoice"`
}

type accountGetObj struct {
	AccountID             string                  `json:"accountID"`
	CreatedAt             time.Time               `json:"createdAt"`
	UpdatedAt             time.Time               `json:"updatedAt"`
	ExpirationDate        time.Time               `json:"expirationDate" validate:"required"`
	MonthsInSubscription  int                     `json:"monthsInSubscription" validate:"required,gte=1" example:"12"`                                                        // number of months in their subscription
	StorageLimit          models.StorageLimitType `json:"storageLimit" validate:"required,gte=10" example:"100"`                                                              // how much storage they are allowed, in GB
	StorageUsed           float64                 `json:"storageUsed" validate:"" example:"30"`                                                                               // how much storage they have used, in GB
	EthAddress            string                  `json:"ethAddress" validate:"required,len=42" minLength:"42" maxLength:"42" example:"a 42-char eth address with 0x prefix"` // the eth address they will send payment to
	Cost                  float64                 `json:"cost" validate:"omitempty,gte=0" example:"2.00"`
	ApiVersion            int                     `json:"apiVersion" validate:"required,gte=1"`
	TotalFolders          int                     `json:"totalFolders" validate:"" example:"2"`
	TotalMetadataSizeInMB float64                 `json:"totalMetadataSizeInMB" validate:"" example:"1.245765432"`
	MaxFolders            int                     `json:"maxFolders" validate:"" example:"2000"`
	MaxMetadataSizeInMB   int64                   `json:"maxMetadataSizeInMB" validate:"" example:"200"`
}

type accountGetReqObj struct {
	Timestamp int64 `json:"timestamp" validate:"required"`
}

type getAccountDataReq struct {
	verification
	requestBody
	accountGetReqObj accountGetReqObj
}

type accountUpdateApiVersionObj struct {
	AccountID string `json:"account_id" binding:"required"`
}

type accountUpdateApiVersionReq struct {
	verification
	requestBody
	accountUpdateApiVersionObj accountUpdateApiVersionObj
}

func (v *accountCreateReq) getObjectRef() interface{} {
	return &v.accountCreateObj
}

func (v *getAccountDataReq) getObjectRef() interface{} {
	return &v.accountGetReqObj
}

func (v *accountUpdateApiVersionReq) getObjectRef() interface{} {
	return &v.accountUpdateApiVersionObj
}

// CreateAccountHandler godoc
// @Summary create an account
// @Description create an account
// @Accept  json
// @Produce  json
// @Param accountCreateReq body routes.accountCreateReq true "account creation object"
// @description requestBody should be a stringified version of (values are just examples):
// @description {
// @description 	"storageLimit": 100,
// @description 	"durationInMonths": 12,
// @description }
// @Success 200 {object} routes.accountCreateRes
// @Failure 400 {string} string "bad request, unable to parse request body: (with the error)"
// @Failure 503 {string} string "error encrypting private key: (with the error)"
// @Router /api/v1/accounts [post]
/*CreateAccountHandler is a handler for post requests to create accounts*/
func CreateAccountHandler() gin.HandlerFunc {
	return ginHandlerFunc(createAccount)
}

// CheckAccountPaymentStatusHandler godoc
// @Summary check the payment status of an account
// @Description check the payment status of an account
// @Accept  json
// @Produce  json
// @Param getAccountDataReq body routes.getAccountDataReq true "account payment status check object"
// @description requestBody should be a stringified version of (values are just examples):
// @description {
// @description 	"timestamp": 1557346389
// @description }
// @Success 200 {object} routes.accountDataRes
// @Success 200 {object} routes.accountUnpaidRes
// @Failure 400 {string} string "bad request, unable to parse request body: (with the error)"
// @Failure 404 {string} string "no account with that id: (with your accountID)"
// @Router /api/v1/account-data [post]
/*CheckAccountPaymentStatusHandler is a handler for requests checking the payment status*/
func CheckAccountPaymentStatusHandler() gin.HandlerFunc {
	return ginHandlerFunc(checkAccountPaymentStatus)
}

// AccountUpdateApiVersionHandler godoc
// @Summary update the account api version to v2
// @Description update the account api version to v2
// @Accept json
// @Produce json
// @Param getAccountDataReq body routes.getAccountDataReq true "account object"
// @description requestBody should be a stringified version of (values are just examples):
// @description {
// @description 	"timestamp": 1659325302
// @description }
// @Success 200 {object} routes.StatusRes
// @Failure 400 {string} string "bad request, unable to parse request body: (with the error)"
// @Failure 404 {string} string "no account with that id: (with your accountID)"
// @Router /api/v2/account/updateApiVersion [post]
/*AccountUpdateApiVersionHandler is a handler for requests updating the account api version to v2*/
func AccountUpdateApiVersionHandler() gin.HandlerFunc {
	return ginHandlerFunc(accountUpdateApiVersionWithContext)
}

func accountUpdateApiVersionWithContext(c *gin.Context) error {
	request := getAccountDataReq{}
	if err := verifyAndParseBodyRequest(&request, c); err != nil {
		return err
	}

	account, err := request.getAccount(c)
	if err != nil {
		return err
	}
	account.ApiVersion = 2

	if err := models.DB.Save(&account).Error; err != nil {
		return InternalErrorResponse(c, err)
	}

	return OkResponse(c, StatusRes{
		Status: "account apiVersion updated to v2",
	})
}

func createAccount(c *gin.Context) error {
	if !utils.WritesEnabled() {
		return ServiceUnavailableResponse(c, errMaintenance)
	}

	request := accountCreateReq{}

	if err := verifyAndParseBodyRequest(&request, c); err != nil {
		return err
	}

	ethAddr, privKey := services.GenerateWallet()

	if err := verifyValidStorageLimit(request.accountCreateObj.StorageLimit, c); err != nil {
		return err
	}

	accountId, err := request.getAccountId(c)
	if err != nil {
		return err
	}

	encryptedKeyInBytes, encryptErr := utils.EncryptWithErrorReturn(
		utils.Env.EncryptionKey,
		privKey,
		accountId,
	)

	if encryptErr != nil {
		return ServiceUnavailableResponse(c, fmt.Errorf("error encrypting private key:  %v", encryptErr))
	}

	account := models.Account{
		AccountID:            accountId,
		StorageLimit:         models.StorageLimitType(request.accountCreateObj.StorageLimit),
		EthAddress:           ethAddr.String(),
		EthPrivateKey:        hex.EncodeToString(encryptedKeyInBytes),
		PaymentStatus:        models.InitialPaymentInProgress,
		ApiVersion:           2,
		MonthsInSubscription: request.accountCreateObj.DurationInMonths,
		ExpiredAt:            time.Now().AddDate(0, request.accountCreateObj.DurationInMonths, 0),
	}

	// Add account to DB
	if err := models.DB.Create(&account).Error; err != nil {
		return BadRequestResponse(c, err)
	}

	cost, err := account.Cost()
	if err != nil {
		return BadRequestResponse(c, err)
	}

	response := accountCreateRes{
		Invoice: models.Invoice{
			Cost:       cost,
			EthAddress: ethAddr.String(),
		},
		ExpirationDate: account.ExpirationDate(),
	}

	return OkResponse(c, response)
}

func checkAccountPaymentStatus(c *gin.Context) error {
	request := getAccountDataReq{}
	if err := verifyAndParseBodyRequest(&request, c); err != nil {
		return err
	}

	account, err := request.getAccount(c)
	if err != nil {
		return err
	}

	pending := false
	paid, networkID, err := account.CheckIfPaid()

	if !paid && err == nil {
		pending = account.CheckIfPending()
	}

	cost, _ := account.Cost()

	var res accountDataRes
	chargePaid := false

	stripePayment, _ := models.GetStripePaymentByAccountId(account.AccountID)
	if len(stripePayment.AccountID) != 0 {
		chargePaid, err = checkChargePaid(c, stripePayment)
		if err != nil {
			return err
		}
		_, err = stripePayment.CheckAccountCreationOPCTTransaction()
		if err != nil {
			return InternalErrorResponse(c, err)
		}
		amount, err := checkChargeAmount(c, stripePayment.ChargeID)
		if err != nil {
			return err
		}
		res.StripeData = stripeDataObj{
			StripeToken:         stripePayment.StripeToken,
			OpctTxStatus:        models.OpctTxStatusMap[stripePayment.OpctTxStatus],
			StripePaymentExists: true,
			ChargePaid:          chargePaid,
			ChargeID:            stripePayment.ChargeID,
			Amount:              amount,
		}
	}

	accountStillActive := verifyAccountStillActive(account)

	res.PaymentStatus = createPaymentStatusResponse(paid, pending, chargePaid, accountStillActive)
	res.Error = err
	res.Account = accountGetObj{
		AccountID:             account.AccountID,
		CreatedAt:             account.CreatedAt,
		UpdatedAt:             account.UpdatedAt,
		ExpirationDate:        account.ExpirationDate(),
		MonthsInSubscription:  account.MonthsInSubscription,
		StorageLimit:          account.StorageLimit,
		StorageUsed:           float64(account.StorageUsedInByte) / 1e9,
		EthAddress:            account.EthAddress,
		Cost:                  cost,
		ApiVersion:            account.ApiVersion,
		TotalFolders:          account.TotalFolders,
		TotalMetadataSizeInMB: float64(account.TotalMetadataSizeInBytes) / 1e6,
		MaxFolders:            utils.Env.Plans[int(account.StorageLimit)].MaxFolders,
		MaxMetadataSizeInMB:   utils.Env.Plans[int(account.StorageLimit)].MaxMetadataSizeInMB,
	}

	if res.PaymentStatus == Paid {
		account.UpdateNetworkIdPaid(networkID)
		return OkResponse(c, res)
	}

	return OkResponse(c, accountUnpaidRes{
		accountDataRes: res,
		Invoice: models.Invoice{
			Cost:       cost,
			EthAddress: account.EthAddress,
		},
	})
}

func createPaymentStatusResponse(paid bool, pending bool, chargePaid bool, stillActive bool) string {
	if !stillActive {
		return Expired
	}
	if paid || chargePaid {
		return Paid
	}
	if pending {
		return Pending
	}
	return Unpaid
}
