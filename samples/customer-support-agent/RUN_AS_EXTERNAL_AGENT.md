# Running the Customer Support Agent as an External Agent

This guide shows how to run the [Customer Support Agent sample](https://github.com/wso2/agent-manager/tree/main/samples/customer-support-agent)
on your own machine (a "bring-your-own" / externally-hosted agent) and stream traces to WSO2 Agent Manager
using zero-code auto-instrumentation (`amp-instrument`).

## Prerequisites

- Python 3.10+ (3.11 recommended)
- An Agent Manager instance reachable locally, with the OTel ingest endpoint port-forwarded
  (default in these samples: `http://localhost:22893/otel`)
- An **agent API key** generated for your agent in Agent Manager
- An **OpenAI API key** (the agent uses GPT models)
- *(Optional)* A **Tavily API key** for the web-search tool — a dummy value works if you don't need search
- *(Optional)* A **PostgreSQL** database with `db_backup.sql` applied, exposed via `DATABASE_URL` —
  required only for the flight/hotel/car-rental/excursion tools; the server starts fine without it

## Steps

### 1. Get the code

```bash
git clone https://github.com/wso2/agent-manager.git
cd agent-manager/samples/customer-support-agent
```

### 2. Create a virtual environment and install dependencies

```bash
python3 -m venv .venv
source .venv/bin/activate          # Windows: .venv\Scripts\activate
pip install --upgrade pip
pip install -r requirements.txt amp-instrumentation
```

> If LangGraph/LangChain spans don't show up and you see
> `Error initializing LangChain instrumentor: wrap_function_wrapper() got an unexpected keyword argument 'module'`,
> pin `wrapt` below 2.0: `pip install "wrapt<2.0"`.

### 3. Configure the agent's own secrets

Create a `.env` file in this directory (it is loaded automatically by the app):

```env
OPENAI_API_KEY=<your-openai-api-key>
TAVILY_API_KEY=<your-tavily-api-key-or-a-dummy-value>
# DATABASE_URL=postgresql://user:password@host:5432/dbname   # optional
```

> Don't commit `.env` — add it to `.gitignore` if it isn't already.

### 4. Set the Agent Manager instrumentation env vars

```bash
export AMP_OTEL_ENDPOINT="http://localhost:22893/otel"   # Agent Manager OTel ingest endpoint
export AMP_AGENT_API_KEY="<your-agent-api-key>"          # generated for this agent in Agent Manager
```

### 5. Run the agent with `amp-instrument`

Wrap the normal start command:

```bash
amp-instrument python main.py
```

The server listens on `http://0.0.0.0:8000`. On startup you should see a line like
`Traceloop exporting traces to http://localhost:22893/otel, authenticating with custom headers`.

### 6. Send a request and view traces

```bash
curl -s -X POST http://localhost:8000/chat \
  -H 'Content-Type: application/json' \
  -d '{"session_id": 1, "message": "Hi, what can you help me with?"}'
```

Then open Agent Manager → your agent → **Observability → Traces** to see the captured LLM/tool spans.

## Notes

- With no `DATABASE_URL`, conversational prompts work, but anything that triggers a DB-backed tool
  (e.g. "what flights do I have booked?") will error in that tool call.
- With a dummy `TAVILY_API_KEY`, the web-search tool will fail when invoked.
- To stop the server: `lsof -ti :8000 | xargs kill` (or just `Ctrl+C` in the foreground).

## Reference

- Sample: <https://github.com/wso2/agent-manager/tree/main/samples/customer-support-agent>
- `amp-instrumentation`: <https://github.com/wso2/agent-manager/tree/main/libs/amp-instrumentation>
