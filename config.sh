#!/usr/bin/env bash
# config.sh — Config for zelvinator bot scripts
# Source this from scripts that need these values.
#
# Edit these lists to control which users and orgs the bot watches.

# Users whose @zelvinator mentions will trigger bot actions
WHITELIST_USERS=(
  Hnatekmar
  xbedna
  MichalPustka
  mroncka
)

# GitHub orgs/accounts to search for @zelvinator mentions
TARGET_ORGS=(
  zelvinator
  Hnatekmar
  hnatekmarorg
  Algovectra
)

# Path to Hermes .env file (for GITHUB_TOKEN)
HERMES_ENV="${HERMES_HOME:-$HOME/.hermes}/.env"
