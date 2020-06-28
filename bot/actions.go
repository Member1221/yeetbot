package bot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	discord "github.com/bwmarrin/discordgo"
	"go.mongodb.org/mongo-driver/bson"
)

const helpText = "**Yeetbot**\n" +
	"\n" +
	"**Syntax**\n" +
	"!yeet <command> <args...>\n" +
	"\n" +
	"**Commands**\n" +
	"```\n" +
	" - help               | Shows this help dialog\n" +
	" - timeout            | Gets the timeout (in days) before a user gets kicked\n" +
	" - timeout (days)     | Sets the timeout (in days) before a user gets kicked\n" +
	" - isimmune (mention) | Gets the user's immunity to being kicked\n" +
	" - immune (mention)   | Toggles the user's immunity to being kicked\n" +
	"```"

const cmdTag = "!yeet"

func HandleKickForGuild(session *discord.Session, guild *discord.Guild, guildData GuildData) {

	// Create a cursor over all the users on a server
	cur, err := MongoClient.UsersCollection().Find(context.Background(), bson.D{{"guildId", guild.ID}})
	if err != nil {
		log.Println(err)
	} else {
		defer cur.Close(context.Background())

		// No errors, iterate over all servers
		for cur.Next(context.Background()) {

			var result UserData
			err := cur.Decode(&result)
			if err != nil {
				log.Println(err)
				break
			}

			// Skip users whom are immune
			if result.Immune {
				continue
			}

			// Calculate and check UNIX offsets
			timeOffsetUnix := time.Now().Unix() - result.LastActivity.Unix()
			unixMaxOffset := guildData.MaxDayInactivity * int64((24 * time.Hour.Seconds()))

			if timeOffsetUnix > unixMaxOffset {

				// Proceed to kick the user and add a reason for the audit log
				err = session.GuildMemberDeleteWithReason(guild.ID, result.UserId, fmt.Sprintln("Inactivity for over ", guildData.MaxDayInactivity, " days. (Automated)"))
				if err != nil {
					log.Println(err)
					continue
				}

				// Tell the user that they have been kicked
				channel, err := session.UserChannelCreate(result.UserId)
				if err == nil {
					session.ChannelMessageSend(channel.ID, strings.ReplaceAll(guildData.KickMessage, "%time%", strconv.FormatInt(guildData.MaxDayInactivity, 10)))
				}
			}

		}
	}
}

func HandleUserJoin(session *discord.Session, user *discord.GuildMemberAdd) {

	// User does not exist, create them
	err := CreateUser(user.GuildID, user.User.ID, time.Now().UTC())
	if err != nil {

		// Something bad happened?
		log.Println(err)
		return
	}
}

func HandleUserLeave(session *discord.Session, user *discord.GuildMemberRemove) {

	// Try to delete a user from the db, if it fails it's fine
	_ = DeleteUser(user.GuildID, user.User.ID)
}

func HandleUserVoice(session *discord.Session, state *discord.VoiceStateUpdate) {

	// Get current UTC time
	stamp := time.Now().UTC()

	// Try to get the user
	user, err := GetUser(state.GuildID, state.UserID)
	if err != nil {

		// User does not exist, create them
		err := CreateUser(state.GuildID, state.UserID, stamp)
		if err != nil {

			// Something bad happened?
			log.Println(err)
			return
		}

		return
	}

	// Update the user's activity
	user.UpdateActivity(stamp)
}

func HandleMessage(session *discord.Session, data *discord.MessageCreate) {

	guild, err := session.Guild(data.GuildID)
	if err != nil {
		log.Println(err)
		return
	}

	// If the length of the message is long enough for a command
	// And if a command tag is the first thing, handle that command
	if len(data.Content) > len(cmdTag)+1 &&
		data.Content[0:len(cmdTag)+1] == cmdTag+" " {

		handleCommand(session, data, guild)
	} else {
		// Otherwise update the user data for the message
		// If the user isn't present in db they will be created
		// The owner of the server is immune to this
		if data.Author.ID != guild.OwnerID {
			handleUpdateData(session, data)
		}
	}
}

func HandleSelfJoin(session *discord.Session, data *discord.GuildCreate) {
	// Try adding the guild
	err := CreateGuild(data.Guild.ID)
	if err != nil {

		// Something failed, delete the guild again also leave it
		session.GuildLeave(data.Guild.ID)
		err = DeleteGuild(data.Guild.ID)
		if err != nil {
			log.Println(err)
		}
	}
}

func handleCommand(session *discord.Session, data *discord.MessageCreate, guild *discord.Guild) {

	// Delete commands sent by unaothorized users
	if data.Author.ID != guild.OwnerID {
		session.ChannelMessageDelete(data.ChannelID, data.ID)
		return
	}

	// Split up command by spaces
	command := strings.Split(data.Content[7:], " ")

	// Help text
	if len(command) == 0 || command[0] == "help" {
		session.ChannelMessageSend(data.ChannelID, helpText)
		return
	}

	// Gets the guild data
	guildData, err := GetGuild(data.GuildID)
	if err != nil {

		// Server *probably* doesn't exist in the DB for some reason
		log.Println(err)
		return
	}

	// Get which command is being run
	switch strings.ToLower(command[0]) {
	case "timeout":
		if len(command) == 1 {
			session.ChannelMessageSend(data.ChannelID, fmt.Sprint("**Kick timeout for this server is ", guildData.MaxDayInactivity, "days**"))
			return
		}

		value, err := strconv.ParseInt(command[1], 0, 64)
		if err != nil {
			session.ChannelMessageSend(data.ChannelID, fmt.Sprint("**", err, "**"))
			break
		}

		guildData.UpdateMaxInactivity(value)
		session.ChannelMessageSend(data.ChannelID, fmt.Sprint("**Kick timeout for this server set to ", guildData.MaxDayInactivity, "days**"))
		break

	case "isimmune":
		if len(command) != 2 {
			session.ChannelMessageDelete(data.ChannelID, data.ID)
		}

		member := mentionToMember(session, guild.ID, command[1])
		if member == nil {

			// User was not found
			return
		}

		// Get the guild user
		guildUser, err := guildData.GetUser(member.User.ID)
		if err != nil {
			log.Println(err)
			return
		}

		session.ChannelMessageSend(data.ChannelID, fmt.Sprint("User immunity is set to: ", guildUser.Immune))
		break

	case "immune":
		if len(command) != 2 {
			session.ChannelMessageDelete(data.ChannelID, data.ID)
		}

		member := mentionToMember(session, guild.ID, command[1])
		if member == nil {

			// User was not found
			return
		}

		// Get the guild user
		guildUser, err := guildData.GetUser(member.User.ID)
		if err != nil {
			log.Println(err)
			return
		}

		guildUser.UpdateImmunity(!guildUser.Immune)
		session.ChannelMessageSend(data.ChannelID, fmt.Sprint(member.Mention(), "had their immunity is set to: ", guildUser.Immune))
		break

	default:
		session.ChannelMessageDelete(data.ChannelID, data.ID)
		break
	}
}

func mentionToMember(session *discordgo.Session, guildId, mention string) *discordgo.Member {

	// It wasn't a mention after all
	if mention[:2] != "<@" {
		return nil
	}

	mention = mention[2:]

	// It's a nickname mention
	if mention[0:1] == "!" {
		mention = mention[1:]
	}

	member, err := session.GuildMember(guildId, mention[:len(mention)-1])
	if err != nil {
		fmt.Println(err)
		return nil
	}

	return member
}

func handleUpdateData(session *discord.Session, data *discord.MessageCreate) {

	// Parse time stamp
	stamp, err := data.Timestamp.Parse()
	if err != nil {
		log.Println(err)
		return
	}

	// Try to get the user
	user, err := GetUser(data.GuildID, data.Author.ID)
	if err != nil {

		// User does not exist, create them
		err := CreateUser(data.GuildID, data.Author.ID, stamp)
		if err != nil {

			// Something bad happened?
			log.Println(err)
			return
		}

		return
	}

	// Update the user's activity
	user.UpdateActivity(stamp)
}
