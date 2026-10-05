// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

const delegateClearGoalDecisionPrompt = "Your goal was cleared. Decide for yourself whether to stop your own helpers and clear their goals; neither happens automatically. If you clear a helper's goal, that helper must decide for itself whether to stop its own helpers and clear their goals, and so on at each level."

const delegateClearGoalDescription = "action=\"clear_goal\" clears a child's goal by session_id, even while it is working, without cascading to its descendants; the child is told and decides for itself whether to stop its own helpers and clear their goals. "

const delegateClearGoalActionDescription = " \"clear_goal\" clears a child's goal without cascading; the child decides for itself whether to stop its own helpers and clear their goals."

const delegateClearGoalSessionIDDescription = " Required for clear_goal, including while the child is working."
