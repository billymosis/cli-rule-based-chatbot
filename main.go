package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// --- 1. Data Structures (In-Memory) ---

// FlowDefinition represents the entire bot flow graph
type FlowDefinition struct {
	ID          string // Unique ID for the flow
	Name        string
	StartNodeID string
	Nodes       []Node
	Edges       []Edge
}

// Node represents a single step or block in the flow
type Node struct {
	ID   string
	Type string         // e.g., "start", "send_message", "receive_input", "conditional", "end"
	Data map[string]any // Flexible data for node (e.g., message text, variable name, conditions)
}

// Edge represents a connection between two nodes
type Edge struct {
	ID           string
	Source       string // ID of the source node
	Target       string // ID of the target node
	SourceHandle string // Optional: for branching from a source node (e.g., button payload)
}

// ConversationSession represents the current state of a user's conversation
type ConversationSession struct {
	UserID           string            // Unique identifier for the user (e.g., CLI user)
	ActiveFlow       *FlowDefinition   // Pointer to the currently active flow definition
	CurrentNodeID    string            // The ID of the node currently being processed/awaited
	SessionVariables map[string]string // Key-value pairs for conversation variables (simple string for now)
	WaitingForInput  bool              // True if the bot is waiting for user input
	ExpectedPayloads []string          // Specific payloads expected if buttons were sent
	MessageHistory   []MessageRecord   // Simple history for context
	LastActivityAt   time.Time
}

// MessageRecord for history (simplified)
type MessageRecord struct {
	Sender string
	Text   string
	Time   time.Time
}

// --- 2. In-Memory "Database" / Global State ---
// For a CLI app, we can just use a global variable to simulate a single user's session.
var currentSession *ConversationSession

// --- 3. Flow Definitions (Hardcoded for simplicity) ---

var welcomeFlow = FlowDefinition{
	ID:          "flow_welcome_1",
	Name:        "Welcome Flow",
	StartNodeID: "start_node",
	Nodes: []Node{
		{ID: "start_node", Type: "start", Data: nil},
		{ID: "greet_user", Type: "send_message", Data: map[string]any{"message": "Hello there! What's your name?", "message_type": "text"}},
		{ID: "get_name", Type: "receive_input", Data: map[string]any{"variable_name": "userName"}},
		{ID: "reply_name", Type: "send_message", Data: map[string]any{"message": "Nice to meet you, {{userName}}! I'm a simple chatbot. How can I help you?", "message_type": "text"}},
		{ID: "ask_interest", Type: "send_message", Data: map[string]any{
			"message":      "Are you interested in our products or need support?",
			"message_type": "button",
			"buttons": []map[string]string{
				{"text": "Products", "payload": "interested_products"},
				{"text": "Support", "payload": "interested_support"},
				{"text": "Just Chat", "payload": "just_chat"},
			},
		}},
		{ID: "handle_interest", Type: "conditional", Data: map[string]any{
			"variable_to_check": "lastUserPayload",
			"conditions": []map[string]any{
				{"operator": "equals", "value": "interested_products", "next_node_id": "products_info"},
				{"operator": "equals", "value": "interested_support", "next_node_id": "support_info"},
				{"operator": "equals", "value": "just_chat", "next_node_id": "chat_with_llm"}, // New LLM node
				{"operator": "default", "next_node_id": "reprompt_interest"},
			},
		}},
		{ID: "products_info", Type: "send_message", Data: map[string]any{"message": "Great! We have a range of amazing products. What type are you looking for?", "message_type": "text"}},
		{ID: "support_info", Type: "send_message", Data: map[string]any{"message": "Please describe your support issue, and I'll try to connect you to the right resource.", "message_type": "text"}},
		{ID: "reprompt_interest", Type: "send_message", Data: map[string]any{"message": "Please choose from 'Products', 'Support', or 'Just Chat'.", "message_type": "text"}},
		{ID: "chat_with_llm", Type: "llm_call", Data: map[string]any{}}, // Simple LLM call
		{ID: "end_flow", Type: "end", Data: nil},
	},
	Edges: []Edge{
		{ID: "e1", Source: "start_node", Target: "greet_user"},
		{ID: "e2", Source: "greet_user", Target: "get_name"},
		{ID: "e3", Source: "get_name", Target: "reply_name"},
		{ID: "e4", Source: "reply_name", Target: "ask_interest"},
		{ID: "e5", Source: "ask_interest", Target: "handle_interest"}, // This is the crucial link: ask_interest -> handle_interest
		{ID: "e6", Source: "handle_interest", SourceHandle: "interested_products", Target: "products_info"},
		{ID: "e7", Source: "handle_interest", SourceHandle: "interested_support", Target: "support_info"},
		{ID: "e8", Source: "handle_interest", SourceHandle: "just_chat", Target: "chat_with_llm"},
		{ID: "e9", Source: "handle_interest", SourceHandle: "default", Target: "reprompt_interest"},
		{ID: "e10", Source: "products_info", Target: "end_flow"},
		{ID: "e11", Source: "support_info", Target: "end_flow"},
		{ID: "e12", Source: "reprompt_interest", Target: "ask_interest"}, // Loop back
		{ID: "e13", Source: "chat_with_llm", Target: "end_flow"},         // LLM will just chat and end for now
	},
}

// --- 4. Core Bot Logic Functions ---

// findNodeInFlow helper function
func findNodeInFlow(flowDef *FlowDefinition, nodeID string) *Node {
	for i := range flowDef.Nodes {
		if flowDef.Nodes[i].ID == nodeID {
			return &flowDef.Nodes[i]
		}
	}
	return nil
}

// findEdgeInFlow helper function (can be for simple next, or for specific handles)
func findEdgeInFlow(flowDef *FlowDefinition, sourceNodeID string, sourceHandle string) *Edge {
	for i := range flowDef.Edges {
		if flowDef.Edges[i].Source == sourceNodeID {
			// If a specific handle is provided, it must match
			// If no handle provided (empty string), it means any outgoing edge from source (e.g., for simple sequence)
			if sourceHandle == "" || flowDef.Edges[i].SourceHandle == sourceHandle {
				return &flowDef.Edges[i]
			}
		}
	}
	return nil
}

// evaluateCondition helper for conditional nodes (very basic string comparison)
func evaluateCondition(value string, operator string, targetValue string) bool {
	switch operator {
	case "equals":
		return value == targetValue
	case "contains":
		return strings.Contains(value, targetValue)
	// Add more operators as needed
	default:
		return false
	}
}

// templateEngine: Simple string replacement for now
func renderTemplate(templateString string, data map[string]string) string {
	rendered := templateString
	for key, val := range data {
		rendered = strings.ReplaceAll(rendered, "{{"+key+"}}", val)
	}
	return rendered
}

// simulateLLMCall: Dummy LLM for CLI
func simulateLLMCall(history []MessageRecord, input string) string {
	fmt.Println("[LLM]: (Simulating AI thinking...)")
	// Very basic response based on simple keywords for demo
	if strings.Contains(strings.ToLower(input), "hello") || strings.Contains(strings.ToLower(input), "hi") {
		return "Hello there! How can I help you today?"
	}
	if strings.Contains(strings.ToLower(input), "weather") {
		return "I am just a simple bot, I don't know the weather!"
	}
	return "That's interesting! Can you tell me more?"
}

// --- THE CORE: processNode function ---
// This function moves the bot through the flow one node at a time.
// It's called recursively for nodes that don't require user input.
// It returns true if the flow should continue processing, false if it's waiting for input or ended.
func processNode(session *ConversationSession) bool {
	currentNode := findNodeInFlow(session.ActiveFlow, session.CurrentNodeID)

	if currentNode == nil {
		fmt.Printf("[BOT]: Internal error: Node '%s' not found. Ending conversation.\n", session.CurrentNodeID)
		session.ActiveFlow = nil // End flow
		session.CurrentNodeID = ""
		session.WaitingForInput = false
		return false // Stop processing
	}

	fmt.Printf("[ENGINE]: Processing node: %s (Type: %s)\n", currentNode.ID, currentNode.Type)

	switch currentNode.Type {
	case "start":
		// Find the next node directly from the start node
		nextEdge := findEdgeInFlow(session.ActiveFlow, currentNode.ID, "")
		if nextEdge == nil {
			fmt.Println("[BOT]: Flow ended unexpectedly after start node.")
			session.ActiveFlow = nil
			return false
		}
		session.CurrentNodeID = nextEdge.Target
		// Recurse immediately to process the next node
		return processNode(session)

	case "send_message":
		messageBody := currentNode.Data["message"].(string)
		messageType := currentNode.Data["message_type"].(string)

		renderedMessage := renderTemplate(messageBody, session.SessionVariables)
		fmt.Printf("[BOT]: %s\n", renderedMessage)
		session.MessageHistory = append(session.MessageHistory, MessageRecord{
			Sender: "bot", Text: renderedMessage, Time: time.Now(),
		})

		if messageType == "button" {
			buttons := currentNode.Data["buttons"].([]map[string]string)
			session.ExpectedPayloads = make([]string, len(buttons))
			for i, btn := range buttons {
				fmt.Printf("  [%d] %s\n", i+1, btn["text"])
				session.ExpectedPayloads[i] = btn["payload"]
			}
			session.WaitingForInput = true
			return false // Stop processing, waiting for user input (button payload or number)
		} else { // "text" message or other types that don't wait for specific input
			nextEdge := findEdgeInFlow(session.ActiveFlow, currentNode.ID, "")
			if nextEdge == nil {
				fmt.Println("[BOT]: Flow ended after sending message (no next node).")
				session.ActiveFlow = nil
				return false
			}
			session.CurrentNodeID = nextEdge.Target
			// Recurse immediately to process the next node
			return processNode(session)
		}

	case "receive_input":
		fmt.Println("[ENGINE]: Waiting for user input...")
		session.WaitingForInput = true
		session.ExpectedPayloads = []string{} // No specific payloads, just free text
		return false                          // Stop processing, waiting for user input

	case "conditional":
		variableToCheck := currentNode.Data["variable_to_check"].(string)
		conditions := currentNode.Data["conditions"].([]map[string]any)

		// Get the actual value from session variables or a special 'lastUserPayload'
		var valueFromSession string
		if variableToCheck == "lastUserPayload" {
			if val, ok := session.SessionVariables["lastUserPayload"]; ok {
				valueFromSession = val
			}
		} else {
			if val, ok := session.SessionVariables[variableToCheck]; ok {
				valueFromSession = val
			}
		}

		matchedNextNodeID := ""
		for _, cond := range conditions {
			operator := cond["operator"].(string)
			targetValue := cond["value"].(string) // Assuming string comparison
			if evaluateCondition(valueFromSession, operator, targetValue) {
				matchedNextNodeID = cond["next_node_id"].(string)
				break
			}
		}

		if matchedNextNodeID == "" {
			// Fallback to default if no condition matched
			for _, cond := range conditions {
				if cond["operator"].(string) == "default" {
					matchedNextNodeID = cond["next_node_id"].(string)
					break
				}
			}
		}

		if matchedNextNodeID == "" {
			fmt.Println("[BOT]: Conditional logic failed to find a path. Ending conversation.")
			session.ActiveFlow = nil
			return false
		}

		session.CurrentNodeID = matchedNextNodeID
		// Recurse immediately to process the next node
		return processNode(session)

	case "llm_call":
		// For LLM, we'll send the entire history
		// In a real app, you'd filter/summarize history for context window
		llmResponse := simulateLLMCall(session.MessageHistory, session.SessionVariables["lastUserMessage"]) // Pass last user message
		fmt.Printf("[BOT LLM]: %s\n", llmResponse)
		session.MessageHistory = append(session.MessageHistory, MessageRecord{
			Sender: "bot_llm", Text: llmResponse, Time: time.Now(),
		})
		session.SessionVariables["llm_response"] = llmResponse // Store LLM response for later use

		nextEdge := findEdgeInFlow(session.ActiveFlow, currentNode.ID, "")
		if nextEdge == nil {
			fmt.Println("[BOT]: Flow ended after LLM call (no next node).")
			session.ActiveFlow = nil
			return false
		}
		session.CurrentNodeID = nextEdge.Target
		// Recurse immediately to process the next node
		return processNode(session)

	case "end":
		fmt.Println("[BOT]: Conversation ended. Thank you for chatting!")
		session.ActiveFlow = nil // Mark flow as ended
		session.CurrentNodeID = ""
		session.WaitingForInput = false
		session.ExpectedPayloads = []string{}
		return false // Stop processing

	default:
		fmt.Printf("[BOT]: Unknown node type '%s'. Ending conversation.\n", currentNode.Type)
		session.ActiveFlow = nil
		return false
	}
}

// --- 5. Main CLI Loop ---
func main() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("--- Simple CLI Chatbot ---")
	fmt.Println("Type 'quit' to exit.")

	// Initialize a new session for a dummy user
	currentSession = &ConversationSession{
		UserID:           "cli_user_1",
		ActiveFlow:       &welcomeFlow, // Start with the welcome flow
		CurrentNodeID:    welcomeFlow.StartNodeID,
		SessionVariables: make(map[string]string),
		WaitingForInput:  false,
		ExpectedPayloads: []string{},
		MessageHistory:   []MessageRecord{},
		LastActivityAt:   time.Now(),
	}

	// Start processing the initial flow
	// This will run until it hits a `send_message` with buttons or `receive_input`
	processNode(currentSession) // Call once to kick off the flow

	for {
		if currentSession.ActiveFlow == nil {
			fmt.Println("\n[INFO]: Flow has ended. Type anything to start a new default conversation, or 'quit' to exit.")
		}

		fmt.Print("You: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if strings.ToLower(input) == "quit" {
			fmt.Println("Exiting chatbot. Goodbye!")
			break
		}

		// 1. Simulate onMessageReceived part for CLI
		// Update session history and last user message/payload
		currentSession.MessageHistory = append(currentSession.MessageHistory, MessageRecord{
			Sender: "user", Text: input, Time: time.Now(),
		})
		currentSession.LastActivityAt = time.Now()
		currentSession.SessionVariables["lastUserMessage"] = input // Always store last text input

		// If a flow is not active, start the default one (similar to webhook's first message)
		if currentSession.ActiveFlow == nil {
			fmt.Println("[ENGINE]: No active flow, starting default.")
			currentSession.ActiveFlow = &welcomeFlow
			currentSession.CurrentNodeID = welcomeFlow.StartNodeID
			currentSession.SessionVariables = make(map[string]string) // Clear variables for new flow
			currentSession.WaitingForInput = false
			currentSession.ExpectedPayloads = []string{}
			// Initial processNode call for the new flow
			processNode(currentSession)
			continue // Go to next loop iteration for user input
		}

		// 2. Process input based on current state (analogous to webhook's main conditional logic)
		if currentSession.WaitingForInput {
			currentNode := findNodeInFlow(currentSession.ActiveFlow, currentSession.CurrentNodeID)

			if currentNode.Type == "receive_input" {
				variableName := currentNode.Data["variable_name"].(string)
				currentSession.SessionVariables[variableName] = input // Store the user's text input
				currentSession.WaitingForInput = false
				currentSession.ExpectedPayloads = []string{} // Clear any expected payloads

				// Move to the next node and continue processing
				nextEdge := findEdgeInFlow(currentSession.ActiveFlow, currentNode.ID, "")
				if nextEdge != nil {
					currentSession.CurrentNodeID = nextEdge.Target
					processNode(currentSession) // Continue processing
				} else {
					fmt.Println("[BOT]: Flow ended after receiving input (no next node).")
					currentSession.ActiveFlow = nil
				}
			} else if currentNode.Type == "send_message" && currentNode.Data["message_type"].(string) == "button" {
				// User provided text, but we were expecting a button payload.
				// Try to parse input as a number for button selection (1-indexed)
				buttonIndex := -1
				fmt.Sscanf(input, "%d", &buttonIndex)

				if buttonIndex > 0 && buttonIndex <= len(currentSession.ExpectedPayloads) {
					payload := currentSession.ExpectedPayloads[buttonIndex-1]
					currentSession.SessionVariables["lastUserPayload"] = payload // Store the selected payload
					currentSession.WaitingForInput = false
					currentSession.ExpectedPayloads = []string{}

					// --- THE FIX IS HERE ---
					// After handling the button input, the bot needs to advance to the node
					// that will process this payload, which is the 'handle_interest' conditional node.
					// We need to set the `CurrentNodeID` directly to this node,
					// as the `send_message` node itself doesn't have an outgoing edge for specific button payloads.
					// Its outgoing edge 'e5' goes to 'handle_interest' unconditionally.
					// So, we find the node that's *supposed* to handle the choice, which is `handle_interest`.

					// Find the node that is the target of the edge from 'ask_interest' (which is 'handle_interest')
					edgeFromAskInterest := findEdgeInFlow(currentSession.ActiveFlow, "ask_interest", "")
					if edgeFromAskInterest != nil {
						currentSession.CurrentNodeID = edgeFromAskInterest.Target // This should set it to "handle_interest"
						fmt.Printf("[ENGINE]: Moving to node '%s' to handle button payload.\n", currentSession.CurrentNodeID)
						processNode(currentSession) // Process the conditional node
					} else {
						fmt.Println("[BOT]: Internal error: Could not find next logic for button. Resetting.")
						currentSession.ActiveFlow = nil
					}
					// --- END OF FIX ---

				} else if len(currentSession.ExpectedPayloads) > 0 {
					fmt.Println("[BOT]: Please select an option by number or provide valid input.")
					// Keep waiting_for_input true, prompt again
				} else {
					// This case means `waiting_for_input` is true but it's not a `receive_input` or `button` prompt.
					// This could be for a generic LLM chat, or an error.
					fmt.Println("[BOT]: I'm currently processing. Please wait or try again.")
				}
			} else {
				// This case covers scenarios where `waiting_for_input` is true but the node type isn't directly `receive_input`
				// or a `send_message` with buttons. E.g., if you had a custom "wait_for_llm_response" node.
				fmt.Println("[BOT]: I'm currently processing. Please wait or try again.")
			}
		} else {
			// No input expected from the flow. This could be a new, unsolicited message.
			// Similar to the 'else' block in the webhook `onMessageReceived` pseudo-code.
			fmt.Println("[ENGINE]: Unsolicited message received. Falling back to simple LLM chat.")
			llmResponse := simulateLLMCall(currentSession.MessageHistory, input)
			fmt.Printf("[BOT LLM]: %s\n", llmResponse)
			currentSession.MessageHistory = append(currentSession.MessageHistory, MessageRecord{
				Sender: "bot_llm", Text: llmResponse, Time: time.Now(),
			})
			// Since it's a fallback LLM, we'll reset the flow to allow starting over or continue with simple chat
			currentSession.ActiveFlow = nil // End the flow here so next input can restart a new one or continue fallback LLM
		}
	}
}
