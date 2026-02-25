package cleanup

import (
	"fmt"
	"time"

	"github.com/slack-go/slack"
)

// WarningBlocks creates Slack Block Kit blocks for a stale session warning.
func WarningBlocks(sessionKey, channelID, threadTS string, lastActivity time.Time, staleDays int) []slack.Block {
	lastActivityStr := lastActivity.Format("2006-01-02 15:04 UTC")

	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("⏰ *Bu thread %d gündür inaktif.*\nSession kapatılırsa context kaybolur.\n\n*Son aktivite:* %s\n*Thread:* <#%s>",
					staleDays, lastActivityStr, channelID),
				false, false),
			nil, nil,
		),
		slack.NewActionBlock(
			"session_cleanup_actions",
			slack.NewButtonBlockElement(
				fmt.Sprintf("session_keep_%s", sessionKey),
				"keep",
				slack.NewTextBlockObject("plain_text", "✅ Devam Et (7 gün)", false, false),
			),
			slack.NewButtonBlockElement(
				fmt.Sprintf("session_close_%s", sessionKey),
				"close",
				slack.NewTextBlockObject("plain_text", "🗑️ Kapat", false, false),
			),
		),
	}
}

// KeptBlocks creates blocks for the updated message after "Devam Et" is pressed.
func KeptBlocks() []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "✅ Session devam edecek (7 gün daha)", false, false),
			nil, nil,
		),
	}
}

// ClosedBlocks creates blocks for the updated message after "Kapat" is pressed.
func ClosedBlocks() []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "🗑️ Session kapatıldı", false, false),
			nil, nil,
		),
	}
}

// ExpiredBlocks creates blocks for the updated message after auto-close (24h expired).
func ExpiredBlocks() []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "⏰ 24 saat içinde yanıt verilmedi — session otomatik kapatıldı", false, false),
			nil, nil,
		),
	}
}
