package cron

import (
	"time"

	"github.com/mayswind/ezbookkeeping/pkg/core"
	"github.com/mayswind/ezbookkeeping/pkg/services"
)

// RemoveExpiredTokensJob represents the cron job which periodically remove expired user tokens from the database
var RemoveExpiredTokensJob = &CronJob{
	Name:        "RemoveExpiredTokens",
	Description: "Periodically remove expired user tokens from the database.",
	Period: CronJobFixedHourPeriod{
		Hour: 0,
	},
	Run: func(c *core.CronContext) error {
		return services.Tokens.DeleteAllExpiredTokens(c)
	},
}

// CreateScheduledTransactionJob represents the cron job which periodically create transaction by scheduled transaction template
var CreateScheduledTransactionJob = &CronJob{
	Name:        "CreateScheduledTransaction",
	Description: "Periodically create transaction by scheduled transaction template.",
	Period: CronJobEvery15MinutesPeriod{
		Second: 0,
	},
	Run: func(c *core.CronContext) error {
		return services.Transactions.CreateScheduledTransactions(c, time.Now().Unix(), c.GetInterval())
	},
}

// CleanupTransactionPicturesJob represents the cron job which periodically recover pending transaction picture objects and clean up orphaned objects
var CleanupTransactionPicturesJob = &CronJob{
	Name:        "CleanupTransactionPictures",
	Description: "Periodically recover pending transaction picture objects and clean up orphaned objects.",
	Period: CronJobIntervalPeriod{
		Interval: time.Hour,
	},
	Run: func(c *core.CronContext) error {
		return services.TransactionPictures.CleanupTransactionPictures(c)
	},
}
