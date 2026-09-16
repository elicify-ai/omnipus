# A first hour with Omnipus

This walkthrough takes you from a fresh install to work you can follow and check. You will meet Mia, build a small team, run a task, and finish a goal.

## What it is

This is a guided first session. It connects the setup steps in [getting started](getting-started.md) with the daily work covered by the rest of the handbook.

Use a real piece of low-risk work as you follow along. A short research summary or draft outline works well because you can judge the result quickly.

## When you would use it

Use this page during your first hour with a new Omnipus install. It is also useful when you want to rehearse the full path before inviting other people to use it.

You need an artificial intelligence provider account or a local provider. Keep its sign-in details or application programming interface key ready. [Settings](settings.md) explains how to change providers later.

## How to complete your first piece of work

1. **Install and start Omnipus.** Follow the method for your computer in [getting started](getting-started.md), then open `http://localhost:5000`.

   **What you see:** A fresh install opens the setup wizard. If you see a sign-in screen instead, this install has already been set up. Sign in or use a different data directory before continuing.

   **If it does not look like that:** Check that the Omnipus process or container is running. If the page does not open, or another app is using port `5000`, follow [troubleshooting](troubleshooting.md).

2. **Create the admin account.** On **What should I call you?**, enter your admin username. Continue to **Set your password**, enter at least eight characters, and enter the same password again.

   **What you see:** The wizard advances to **Add a model key**. Password guidance appears while you type.

   **If it does not look like that:** Remove any blank username. If you cannot continue from the password screen, check its length and make sure both entries match.

3. **Connect a provider.** Choose a provider on **Add a model key**. Use its sign-in option when offered, or enter its key. Choose a model for your first agent and run **Check connection**.

   **What you see:** A successful check unlocks **Finish**. Select it to create the account, save the provider, and sign you in. The last screen introduces **Mia — Assistant**.

   **If it does not look like that:** Read the message beside the connection check. Recopy a rejected key, complete any provider sign-in, or check the custom endpoint when the provider requires one. A local provider must already be running. The [settings](settings.md) page owns the full provider guide.

4. **Have your first conversation with Mia.** Select **Start chatting**. In **My Workspace**, type: `Give me three useful ways to test this workspace.` Send the message.

   **What you see:** The **Chat** tab opens with Mia selected in the agent picker. Mia's answer appears in the conversation.

   **If it does not look like that:** If the composer says it is connecting, wait for the connection before sending. If sending fails, return to the provider check in Settings and confirm that the selected model is available.

5. **Create a workspace for real work.** Open the sidebar and select **New workspace**. Name it `First project` and confirm the name.

   **What you see:** Omnipus opens the new workspace on its **Chat** tab. Its other tabs are **Tasks**, **Calendar**, **Library**, and **Team**. Select the workspace name to open its Settings. [Workspaces](workspaces.md) explains what belongs inside one.

   **If it does not look like that:** A blank name cannot be saved. If creation fails, keep working in **My Workspace** and retry after checking the error message.

6. **Create a teammate agent.** Open **Agents** from the sidebar and select **+ New Main**. On **Identity**, give the agent a name, short description, and a model from your connected provider. On **Personality**, write a direct instruction such as `You turn research into short, factual briefings.` Review **Tools**, then select **Create agent**.

   **What you see:** The new agent appears in the Agents list. The wizard shows **Identity**, **Personality**, and **Tools** as its three steps.

   **If it does not look like that:** **Next** stays unavailable until the required fields on that step are complete. If no model appears, reconnect a provider in Settings. If creation fails, leave the advanced options unchanged and retry with the required fields only. See [agents](agents.md) before choosing a different agent type.

7. **Add the agent to your workspace team.** Return to `First project`, open **Team**, and select **Add agent**. Choose the agent you created.

   **What you see:** The agent appears in **Team & delegation**. It also becomes available where the workspace asks you to choose an agent.

   **If it does not look like that:** The picker only offers agents that are not already on the team. Return to **Agents** and confirm that your new agent exists, then reopen the Team tab. [Workspaces](workspaces.md) covers team membership and delegation.

8. **Give the agent a task.** Open **Tasks**, choose **Board**, and select **New task**. Enter a short title and a concrete goal. Add at least one acceptance criterion and one definition-of-done item. Choose your new agent, then select **Create & Run**.

   For example, use the title `Draft a customer interview brief`, the goal `Write a one-page brief for a 20-minute customer interview`, and criteria that require five questions and a short opening script.

   **What you see:** The task appears as a card on the Board and starts running. The Board groups cards by status, so you can follow the card as the work changes from **In Progress** to its final state.

   **If it does not look like that:** The form requires a title, goal, acceptance criterion, and definition-of-done item. If your agent is missing from the picker, add it on **Team** first. If you chose **Create** instead, open the card and use its start control. [Tasks](tasks.md) explains the fields and statuses.

9. **Watch the task finish.** Keep the Board open and select the task card when you want its detail. Check its latest activity, result, acceptance criteria, and definition of done.

   **What you see:** The card and detail view update as the run advances. A successful task reaches **Done**. A problem can leave it **Blocked** or **Failed**, with detail you can use for the next decision.

   **If it does not look like that:** Refresh the workspace if the Board stops updating. Open the task detail before rerunning anything: it may be waiting for an approval or information from you. Use [tasks](tasks.md) for recovery choices.

10. **Set one goal in chat.** Return to the workspace **Chat** tab, choose Mia or your new agent, and send a goal command. For example: `/goal Produce a final customer interview brief with five questions and an opening script.`

    **What you see:** The goal starts immediately. Chat shows goal-specific progress while the agent frames the goal and its acceptance criteria. The active goal remains visible while work continues.

    **If it does not look like that:** The command must begin the message and include the goal after `/goal`. A message containing `/goal` later in a sentence does not set one. Send `/goal` by itself to check the current goal rather than creating another one.

11. **Read the outcome.** Stay in the conversation while the agent works. Respond if it asks for information or approval.

    **What you see:** The conversation ends with an outcome such as **Goal met** or **Goal not met**. The result includes the goal text, so you can connect the verdict to what you asked for. [Goals](goals.md) explains how the Judge checks the work and what each ending means.

    **If it does not look like that:** A waiting goal needs your reply before it can continue. A not-met outcome is not the same as a lost run; read the reason and revise the request or inputs before starting a new goal.

The table below shows the trail you should have after this walkthrough.

| You created | Where you can find it | What confirms it worked |
|---|---|---|
| Admin account and provider | Settings | You can sign in and Mia can answer |
| `First project` workspace | Sidebar | Its Chat, Tasks, Calendar, Library, Team, and Settings tabs open |
| Teammate agent | Agents and the workspace Team tab | The agent is available for workspace work |
| First task | Workspace Tasks tab | Its Board card reaches a final status |
| First goal | Workspace Chat tab | Chat reports a met or not-met outcome |

## Limits and things to watch

- This walkthrough uses a small, reversible task. Review provider permissions, agent tools, and workspace access before assigning sensitive work.
- A task and a goal are related but different. The task is a Board item. The chat goal is a running outcome that the Judge checks. Follow [tasks](tasks.md) and [goals](goals.md) for their separate controls.
- Creating an agent does not add it to every workspace. Add it from the workspace's Team tab where you want it to work.
- Provider errors can appear after setup if a key expires, a model becomes unavailable, or the provider limits requests. Recheck the provider in Settings before changing the task.
- **Done** is final for a task. Read the task detail and result before deciding whether you need a new task.

## Related pages

- [getting started](getting-started.md) — installation, first-run setup, and the first chat in detail
- [workspaces](workspaces.md) — where each team's conversations and work live
- [agents](agents.md) — agent types, models, tools, and team membership
- [tasks](tasks.md) — creating, assigning, running, and tracking Board work
- [goals](goals.md) — starting a goal and understanding its checked outcome
- [settings](settings.md) — providers, models, and install-wide controls
