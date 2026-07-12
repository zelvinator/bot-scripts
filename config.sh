#!/usr/bin/env bash
# config.sh — Config for zelvinator bot scripts
# Source this from scripts that need these values.
#
# The bot no longer watches specific orgs. Instead, it searches ALL repos
# the bot token has access to. Invite the zelvinator GitHub user as a
# collaborator to any repo/org, then interact by:
#   - Mentioning @zelvinator in an issue, PR, or comment
#   - Assigning an issue to the zelvinator user
#
# Only whitelisted users' @zelvinator mentions trigger bot actions.

# Users whose @zelvinator mentions will trigger bot actions
WHITELIST_USERS=(
  Hnatekmar
  xbedna
  MichalPustka
  mroncka
)

# Path to Hermes .env file (for GITHUB_TOKEN)
HERMES_ENV="${HERMES_HOME:-$HOME/.hermes}/.env"
