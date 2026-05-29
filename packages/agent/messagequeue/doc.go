// Package messagequeue implements the steering and follow-up message queues
// used by the agent loop. A queue buffers user messages that arrive while the
// agent is busy and drains them either all at once or one at a time depending
// on its configured mode.
package messagequeue
