<!-- Copyright 2026 The Taisce Authors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# Quickstart

In this guide you run Taisce on your machine, save one thing a user said, ask about it, and then
forget it, in seven steps with `curl`. Then, in step 8, you give an agent memory in the framework you
use: Microsoft Agent Framework in Python or .NET, LangGraph, Spring AI, LangChain4j, or a coding agent
over MCP. Each step shows the command, what you should see, and what just happened.

Taisce needs a model to read conversations. The quickstart uses DeepSeek's hosted API: there is
nothing to download or serve. To keep conversation text on your own infrastructure in production,
serve Qwen with vLLM, as [choosing a model](../architecture/deployment.md#choosing-a-model) describes.

## What you need

- **Docker with the Compose plugin**, so `docker compose version` answers. The older standalone
  `docker-compose` will not do.
- **A [DeepSeek API key](https://platform.deepseek.com/api_keys).** An
  [OpenRouter key](https://openrouter.ai/keys) is only needed later, if you turn on semantic search.
- **`jq`**, to pull fields out of JSON, and **`uuidgen`**.
- **An empty directory** to work in. You do not need a checkout and you do not need Go: the compose
  file names images a release published, for `linux/amd64` and `linux/arm64`, and pulls them.

[Set up your machine](../start/your-machine.md) gets you Docker, `jq` and `uuidgen` on Linux, macOS
with Colima and Windows with WSL2.

## 1. Give Taisce a model

Taisce uses a model to read each conversation turn and propose facts. In the empty directory you will
run step 2 from, write your DeepSeek key and the model's settings to `.env`. Compose reads that file
from the directory on every command, so nothing has to stay exported:

```bash
export DEEPSEEK_API_KEY=…                       # from platform.deepseek.com
cat > .env <<EOF
TAISCE_INFERENCE_API_KEY=$DEEPSEEK_API_KEY
TAISCE_INFERENCE_ENDPOINT=https://api.deepseek.com/v1
TAISCE_INFERENCE_EXTRACTOR_MODEL=deepseek-flash
TAISCE_INFERENCE_ALLOWLIST=api.deepseek.com
EOF
chmod 600 .env
curl -sS https://api.deepseek.com/models -H "Authorization: Bearer $DEEPSEEK_API_KEY" | jq -r '.data[].id'
```

You should see the models your key can use:

```text
deepseek-flash
deepseek-v4-pro
```

**What this sends where.** Every turn you save is sent to DeepSeek to be read. Only the hosts in
`TAISCE_INFERENCE_ALLOWLIST` ever receive text, so writing `api.deepseek.com` there is you choosing
that. Asking questions and erasing never call a model.

**Semantic search, later.** Forming facts and asking questions need no embedding model. When you want
to search the words people said, add OpenRouter for embeddings (DeepSeek's API has none), list its
host too, and build a generation as [message embeddings](../22-message-embeddings.md) describes:

```bash
export OPENROUTER_API_KEY=…                     # from openrouter.ai/keys
cat >> .env <<EOF
TAISCE_INFERENCE_EMBEDDING_ENDPOINT=https://openrouter.ai/api/v1
TAISCE_INFERENCE_EMBEDDING_MODEL=qwen/qwen3-embedding-4b
TAISCE_INFERENCE_EMBEDDING_API_KEY=$OPENROUTER_API_KEY
EOF
sed -i.bak 's/^TAISCE_INFERENCE_ALLOWLIST=.*/TAISCE_INFERENCE_ALLOWLIST=api.deepseek.com,openrouter.ai/' .env && rm .env.bak
```

**Didn't work?** A `401` from the `curl` means the key is wrong or not exported in this terminal.
`.env` holds that key now: keep it out of version control.

### Which model?

Taisce asks the model for strict JSON built from a fixed list of relations, and not every model can
keep to that. These were measured against the same 19-message test corpus
([extraction models, 2026-09-11](../36-extraction-models.md)):

| Model | Where it runs | Keeps to the format? | Set it up with |
|---|---|---|---|
| `deepseek-flash` | DeepSeek's hosted API | Yes, all 19 cases | step 1 |
| `Qwen/Qwen3.8-27B`, thinking off | vLLM, on your own GPU | Yes, all 19 cases | [choosing a model](../architecture/deployment.md#choosing-a-model), for production |

With a hosted model, what people tell your agent leaves your machine and goes to that provider.
Taisce only sends text to hosts you have listed in the allowlist, so that is always your decision.

A model that is not in this table may still work. Check it with `make test-inference` before you
rely on it. If a model answers but nothing ever forms, see
[memory isn't forming](troubleshooting.md#memory-isnt-forming).
[Choosing a model](../architecture/deployment.md#choosing-a-model) covers running Qwen models in
production.

## 2. Start Taisce

In the directory that holds `.env`, fetch the compose file for the release you want, then bring it
up:

```bash
curl -fsSLO https://raw.githubusercontent.com/ensera-ai/taisce/v0.4.0/compose.yaml
docker compose up -d
docker compose ps -a --format '{{.Service}}\t{{.State}}\t{{.Health}}'
curl -s localhost:8080/ready
```

The first run pulls two images — the service and its PostgreSQL substrate — so give it a minute on a
cold cache. Once the health checks pass, you should see:

```text
api        running  healthy
bootstrap  exited
manage     running  healthy
postgres   running  healthy
worker     running  healthy
{"status":"ready"}
```

Then check that the worker found a model it is allowed to use:

```bash
docker compose logs --no-log-prefix worker | grep -c 'MEMORY WILL NOT FORM'
```

```text
0
```

A one-off `bootstrap` set up PostgreSQL, created a project called `default` and printed its tokens.
Now the `api` answers on port 8080, and the `worker` turns stored turns into facts in the
background.

**Didn't work?** If port 8080 is taken, run `TAISCE_PORT=18080 docker compose up -d` and use that
port below. If the count is `1` or more, see
[memory never forms](troubleshooting.md#the-worker-logs-memory-will-not-form).
If `api` or `worker` keeps restarting, see
[the serving process exits at start](troubleshooting.md#the-api-or-worker-exits-right-after-starting).

## 3. Get your token

```bash
docker compose logs --no-log-prefix bootstrap | grep 'token:'
```

You should see two tokens:

```text
operator token: tsk_…
token: tsk_…
```

Save the second one, the **project token**, and try it:

```bash
export TOKEN=$(docker compose logs --no-log-prefix bootstrap | awk '/^token:/ {print $2}')
curl -sS localhost:8080/v1/freshness -H "Authorization: Bearer $TOKEN"
```

```json
{"scope":"default","stored":null,"formed":null,"parked":0}
```

The project token opens memory for the `default` project, and `null` means nothing has been written
yet. The operator token is for running the instance and is refused on memory calls.

**Didn't work?** A `401` means the token is wrong, and the usual cause is picking up the operator
token by mistake ([every call answers 401](troubleshooting.md#every-call-answers-401-unauthenticated)).
Tokens are printed once, on the first start. If the line is gone, issue a new one with
`docker compose exec manage /taisce credential issue app --project default`.

## 4. Save a memory

```bash
export KEY=$(uuidgen)
curl -sS -X POST localhost:8080/v1/observations \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d @- <<EOF
{"idempotency_key": "$KEY",
 "data_subject_id": "alice",
 "messages": [{"role": "user", "content": "I work at Ensera and I live in Dublin."}]}
EOF
```

You should see a receipt, with HTTP status `201`:

```json
{"id":"…","scope":"default","log_offset":0}
```

Taisce stored Alice's turn and answered straight away, before any model looked at it. `log_offset`
is the turn's place in the project's log, and `data_subject_id` says the words are Alice's. Run the
same command again and you get the same receipt: the `idempotency_key` makes it a retry, not a
second turn.

**Didn't work?** A `400 invalid_turn` says what is wrong in its `message`, such as a key that is not
a UUID. A `409 idempotency_conflict` means the key was already used for something else, so make a
new one with `uuidgen` ([`409 idempotency_conflict`](troubleshooting.md#409-idempotency_conflict)).

## 5. Wait for it to form

```bash
while :; do
  F=$(curl -sS localhost:8080/v1/freshness -H "Authorization: Bearer $TOKEN"); echo "$F"
  case "$F" in *'"formed":null'*) sleep 2 ;; *) break ;; esac
done
```

You should see a few lines while the model works, then `formed` catches up:

```text
{"scope":"default","stored":0,"formed":null,"parked":0}
{"scope":"default","stored":0,"formed":null,"parked":0}
{"scope":"default","stored":0,"formed":0,"parked":0}
```

The worker had the model read Alice's message and kept only the facts whose words it could find in
it. `stored` is the last turn Taisce holds, and `formed` is the last turn it has turned into facts,
so your turn is in memory once `formed` reaches its `log_offset`
([stored and formed](../start/how-it-works.md#stored-and-formed)).

**Didn't work?** If `formed` stays `null`, see
[`formed` stays `null`](troubleshooting.md#formed-stays-null). If the loop ends with `"parked":1`,
the worker gave up on the turn after several tries; see
[turns are parked](troubleshooting.md#turns-are-parked).

## 6. Ask a question

```bash
curl -sS -X POST localhost:8080/v1/recalls \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where do I work?", "data_subject_id": "alice"}' \
  | jq '{anchors, facts: [.facts[] | {predicate, object, quote: .evidence.quote}]}'
```

You should see Alice's facts, each with the words it came from (trimmed; the statements come from
the model, so wording can vary):

```json
{
  "anchors": [{"entity_id": "…", "name": "…", "type": "…", "matched": "speaker"}],
  "facts": [
    {"predicate": "works_at", "object": "Ensera", "quote": "I work at Ensera"},
    {"predicate": "lives_in", "object": "Dublin", "quote": "…"}
  ]
}
```

Because you named Alice as the `data_subject_id`, "I" meant Alice, so recall started from her and
returned her facts, without calling a model. Without a subject, ask by name instead, as in "What do
we know about Ensera?".

**Didn't work?** An empty `facts` usually means the subject is spelled differently from the one you
saved (`Alice` is not `alice`) or `formed` has not reached your turn yet
([recall returns nothing for "I"](troubleshooting.md#questions-about-i-return-nothing)).

## 7. Forget Alice

```bash
curl -sS -X POST localhost:8080/v1/erasures \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"data_subject_id": "alice", "reason": "right to erasure"}' | jq
```

You should see a receipt (trimmed; which kinds appear depends on what formed):

```json
{
  "request_id": "…",
  "scope": "default",
  "data_subject_id": "alice",
  "completed_at": "…",
  "deleted": {"observation": 1, "fact": 2, "entity": …, "chunk": 1, …},
  "residual": {"fact": 0, "entity": 0, "chunk": 0, …},
  "clean": true
}
```

Ask again:

```bash
curl -sS -X POST localhost:8080/v1/recalls \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"question": "Where do I work?", "data_subject_id": "alice"}' | jq '.facts'
```

```text
[]
```

Taisce deleted Alice's turn and everything built only from it. Then, in the same transaction, it
counted what still matched her: that count is `residual`, and `clean` is `true` because every count
is zero.

**Didn't work?** A `400 no_subject` means the body named nobody, and an erasure never defaults to the
whole project. A receipt with `clean: false` names the kind of thing that survived; the
[forget a person](../examples/forget-a-person.md) recipe explains how to read one, and
[troubleshooting](troubleshooting.md) covers the rest of a first run.

## 8. Give an agent memory

Steps 1 to 7 called the API yourself. An adapter makes the same calls from inside your agent
framework: before the model answers, it recalls what Taisce knows about the person and adds it to the
request as one message marked untrusted; after the model answers, it saves the turn.
[Adapters](adapters.md) explains what every adapter holds to.

Each example below is the same small program in a different framework. It takes a message on the
command line, runs one turn for `alice`, prints the reply and exits. You run it twice, and the second
run is a new process with no chat history, so whatever it knows from the first came from Taisce.

Keep Taisce running from step 2. In the terminal where you run the program, set where Taisce is and
which model the agent talks to:

```bash
export TAISCE_API=http://localhost:8080
export TAISCE_TOKEN=$TOKEN                        # the project token from step 3
export MODEL_BASE_URL=https://api.deepseek.com/v1 # any OpenAI-compatible endpoint
export MODEL_NAME=deepseek-flash
export MODEL_API_KEY=$DEEPSEEK_API_KEY
```

The agent's model answers the person; Taisce's model, from step 1, reads what was said. They can be
the same model, as here, or different ones.

Between the two runs, wait for the first turn to form, as in step 5. This waits until `formed` has
caught up with `stored`:

```bash
wait_formed() {
  until curl -sS "$TAISCE_API/v1/freshness" -H "Authorization: Bearer $TAISCE_TOKEN" \
    | jq -e '.formed != null and .formed >= .stored' >/dev/null; do sleep 2; done
}
```

Pick the framework you use. Microsoft Agent Framework comes first, in Python and in .NET.

### Python: Microsoft Agent Framework

`TaisceContextProvider` is a context provider, the framework's own hook for adding to a request.

1. Install the adapter and the framework's OpenAI-compatible client. You need Python 3.10 or newer.

   ```bash
   python -m venv .venv && . .venv/bin/activate
   pip install taisce-agent-framework agent-framework-openai
   ```

2. Save this as `agent.py`:

   ```python
   import asyncio
   import os
   import sys
   import uuid

   from agent_framework import Agent
   from agent_framework.openai import OpenAIChatCompletionClient

   from taisce import Client
   from taisce_agent_framework import TaisceContextProvider


   def report(stage: str, exc: Exception) -> None:
       print(f"taisce {stage} failed: {exc!r}", file=sys.stderr)


   async def main(message: str) -> None:
       model = OpenAIChatCompletionClient(
           os.environ["MODEL_NAME"],
           base_url=os.environ["MODEL_BASE_URL"],
           api_key=os.environ["MODEL_API_KEY"],
       )
       async with Client(os.environ["TAISCE_API"], os.environ["TAISCE_TOKEN"]) as taisce:
           memory = TaisceContextProvider(
               taisce,
               data_subject_id="alice",      # whose memory this is
               run_id=str(uuid.uuid4()),     # this conversation
               on_error=report,
           )
           agent = Agent(model, context_providers=[memory])
           response = await agent.run(message)
           print(response.text)


   asyncio.run(main(" ".join(sys.argv[1:])))
   ```

3. Run it twice:

   ```bash
   python agent.py "I moved to Cork last month for a new job at Ensera."
   wait_formed
   python agent.py "Where do I live now, and where do I work?"
   ```

   The first reply is whatever the model says to news; the second answers from memory (the wording
   will differ):

   ```text
   You live in Cork and work at Ensera.
   ```

### .NET: Microsoft Agent Framework

`TaisceContextProvider` is an `AIContextProvider`, the framework's own hook for adding to a request.

1. Make a console app and add the adapter and an OpenAI-compatible chat client. You need the .NET 10
   SDK.

   ```bash
   dotnet new console -n MemoryAgent && cd MemoryAgent
   dotnet add package Taisce.AgentFramework --version 0.1.2
   dotnet add package Microsoft.Extensions.AI.OpenAI --version 10.10.0
   ```

2. Replace `Program.cs` with:

   ```csharp
   using System.ClientModel;
   using Microsoft.Agents.AI;
   using Microsoft.Extensions.AI;
   using OpenAI;
   using Taisce;
   using Taisce.AgentFramework;

   string Env(string name) => Environment.GetEnvironmentVariable(name)
       ?? throw new InvalidOperationException($"{name} is not set");

   IChatClient model = new OpenAIClient(
           new ApiKeyCredential(Env("MODEL_API_KEY")),
           new OpenAIClientOptions { Endpoint = new Uri(Env("MODEL_BASE_URL")) })
       .GetChatClient(Env("MODEL_NAME"))
       .AsIChatClient();

   using var taisce = new TaisceClient(Env("TAISCE_API"), Env("TAISCE_TOKEN"));

   var memory = new TaisceContextProvider(taisce, new TaisceContextProviderOptions
   {
       DataSubjectId = "alice",                // whose memory this is
       RunId = Guid.NewGuid().ToString(),      // this conversation
       OnError = (stage, ex) => Console.Error.WriteLine($"taisce {stage} failed: {ex.Message}"),
   });

   AIAgent agent = new ChatClientAgent(model, new ChatClientAgentOptions { AIContextProviders = [memory] });
   Console.WriteLine(await agent.RunAsync(string.Join(' ', args)));
   ```

3. Run it twice:

   ```bash
   dotnet run -- "I moved to Cork last month for a new job at Ensera."
   wait_formed
   dotnet run -- "Where do I live now, and where do I work?"
   ```

   ```text
   You live in Cork and you work at Ensera.
   ```

The [.NET guide](dotnet.md) adds the host-builder registration, compaction and saved sessions.

### Python: LangGraph

LangGraph has no context provider; `TaisceMemory` is agent middleware for `create_agent`.

1. Install:

   ```bash
   python -m venv .venv && . .venv/bin/activate
   pip install taisce-langgraph langchain-openai
   ```

2. Save this as `memory_agent.py`:

   ```python
   import asyncio
   import os
   import sys
   import uuid

   from langchain.agents import create_agent
   from langchain_core.messages import HumanMessage
   from langchain_openai import ChatOpenAI

   from taisce import Client
   from taisce_langgraph import TaisceMemory


   def report(stage: str, exc: Exception) -> None:
       print(f"taisce {stage} failed: {exc}", file=sys.stderr)


   async def main(text: str) -> None:
       model = ChatOpenAI(
           base_url=os.environ["MODEL_BASE_URL"],
           api_key=os.environ["MODEL_API_KEY"],
           model=os.environ["MODEL_NAME"],
       )
       async with Client(os.environ["TAISCE_API"], os.environ["TAISCE_TOKEN"]) as client:
           memory = TaisceMemory(client, data_subject_id="alice", run_id=f"run-{uuid.uuid4()}", on_error=report)
           agent = create_agent(model, tools=[], middleware=[memory])
           state = await agent.ainvoke({"messages": [HumanMessage(content=text)]})
           print(state["messages"][-1].content)


   asyncio.run(main(" ".join(sys.argv[1:])))
   ```

   The middleware is async only, so the agent runs with `ainvoke`.

3. Run it twice:

   ```bash
   python memory_agent.py "I moved to Cork last month for a new job at Ensera."
   wait_formed
   python memory_agent.py "Where do I live now, and where do I work?"
   ```

   ```text
   You live in Cork and work at Ensera.
   ```

### Java: Spring AI

`TaisceMemoryAdvisor` is an advisor on a `ChatClient`. This example builds the client in a plain
`main`; in a Spring Boot application the `ChatClient.Builder` comes from your model starter instead.
You need Java 21 and Maven.

1. Make a project with this `pom.xml`. The adapter leaves the Spring AI version to you, so the
   framework is listed next to it:

   ```xml
   <project xmlns="http://maven.apache.org/POM/4.0.0">
     <modelVersion>4.0.0</modelVersion>
     <groupId>example</groupId>
     <artifactId>memory-agent</artifactId>
     <version>1.0</version>

     <properties>
       <maven.compiler.release>21</maven.compiler.release>
       <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
     </properties>

     <dependencies>
       <dependency>
         <groupId>ai.ensera.taisce</groupId>
         <artifactId>taisce-spring-ai</artifactId>
         <version>0.1.2</version>
       </dependency>
       <dependency>
         <groupId>org.springframework.ai</groupId>
         <artifactId>spring-ai-client-chat</artifactId>
         <version>2.0.1</version>
       </dependency>
       <dependency>
         <groupId>org.springframework.ai</groupId>
         <artifactId>spring-ai-openai</artifactId>
         <version>2.0.1</version>
       </dependency>
     </dependencies>

     <build>
       <plugins>
         <plugin>
           <groupId>org.codehaus.mojo</groupId>
           <artifactId>exec-maven-plugin</artifactId>
           <version>3.5.0</version>
           <configuration>
             <mainClass>MemoryAgent</mainClass>
           </configuration>
         </plugin>
       </plugins>
     </build>
   </project>
   ```

2. Save this as `src/main/java/MemoryAgent.java`:

   ```java
   import ai.ensera.taisce.client.TaisceClient;
   import ai.ensera.taisce.springai.TaisceMemoryAdvisor;
   import java.util.Map;
   import java.util.UUID;
   import org.springframework.ai.chat.client.ChatClient;
   import org.springframework.ai.openai.OpenAiChatModel;
   import org.springframework.ai.openai.OpenAiChatOptions;

   public class MemoryAgent {
       public static void main(String[] args) {
           OpenAiChatModel model = OpenAiChatModel.builder()
                   .options(OpenAiChatOptions.builder()
                           .baseUrl(System.getenv("MODEL_BASE_URL"))
                           .apiKey(System.getenv("MODEL_API_KEY"))
                           .model(System.getenv("MODEL_NAME"))
                           .build())
                   .build();
           TaisceClient taisce = new TaisceClient(System.getenv("TAISCE_API"), System.getenv("TAISCE_TOKEN"));
           ChatClient chat = ChatClient.builder(model).build();

           var memory = new TaisceMemoryAdvisor(
                   taisce,
                   "alice",                           // whose memory this is
                   "run-" + UUID.randomUUID(),        // this conversation
                   Map.of(),                          // recall options; empty uses the server's defaults
                   (stage, e) -> System.err.println("taisce " + stage + " failed: " + e.getMessage()));

           String reply = chat.prompt()
                   .user(String.join(" ", args))
                   .advisors(memory)
                   .call()
                   .content();
           System.out.println(reply);
       }
   }
   ```

3. Run it twice:

   ```bash
   mvn -q compile exec:java -Dexec.args="I moved to Cork last month for a new job at Ensera."
   wait_formed
   mvn -q compile exec:java -Dexec.args="Where do I live now, and where do I work?"
   ```

   ```text
   You live in Cork and work at Ensera.
   ```

   Maven may first print `SLF4J(W): No SLF4J providers were found`: no logging backend is on the
   classpath, and nothing else is wrong.

### Java: LangChain4j

`TaisceChatModel` wraps the `ChatModel` you already have, because LangChain4j reads its chat memory
before the new message is in it.

1. Make a project with the `pom.xml` from Spring AI above, with these dependencies in place of its
   `<dependencies>`:

   ```xml
   <dependencies>
     <dependency>
       <groupId>ai.ensera.taisce</groupId>
       <artifactId>taisce-langchain4j</artifactId>
       <version>0.1.2</version>
     </dependency>
     <dependency>
       <groupId>dev.langchain4j</groupId>
       <artifactId>langchain4j</artifactId>
       <version>1.20.0</version>
     </dependency>
     <dependency>
       <groupId>dev.langchain4j</groupId>
       <artifactId>langchain4j-open-ai</artifactId>
       <version>1.20.0</version>
     </dependency>
   </dependencies>
   ```

2. Save this as `src/main/java/MemoryAgent.java`:

   ```java
   import ai.ensera.taisce.client.TaisceClient;
   import ai.ensera.taisce.langchain4j.TaisceChatModel;
   import dev.langchain4j.memory.chat.MessageWindowChatMemory;
   import dev.langchain4j.model.chat.ChatModel;
   import dev.langchain4j.model.openai.OpenAiChatModel;
   import dev.langchain4j.service.AiServices;
   import java.util.Map;
   import java.util.UUID;

   public class MemoryAgent {
       interface Assistant {
           String chat(String message);
       }

       public static void main(String[] args) {
           ChatModel model = OpenAiChatModel.builder()
                   .baseUrl(System.getenv("MODEL_BASE_URL"))
                   .apiKey(System.getenv("MODEL_API_KEY"))
                   .modelName(System.getenv("MODEL_NAME"))
                   .build();
           TaisceClient taisce = new TaisceClient(System.getenv("TAISCE_API"), System.getenv("TAISCE_TOKEN"));

           ChatModel withMemory = new TaisceChatModel(
                   model,
                   taisce,
                   "alice",                           // whose memory this is
                   "run-" + UUID.randomUUID(),        // this conversation
                   Map.of(),                          // recall options; empty uses the server's defaults
                   (stage, e) -> System.err.println("taisce " + stage + " failed: " + e.getMessage()));

           Assistant assistant = AiServices.builder(Assistant.class)
                   .chatModel(withMemory)
                   .chatMemory(MessageWindowChatMemory.withMaxMessages(20))
                   .build();

           System.out.println(assistant.chat(String.join(" ", args)));
       }
   }
   ```

3. Run it twice, the same way as Spring AI:

   ```bash
   mvn -q compile exec:java -Dexec.args="I moved to Cork last month for a new job at Ensera."
   wait_formed
   mvn -q compile exec:java -Dexec.args="Where do I live now, and where do I work?"
   ```

   ```text
   You live in Cork and work at Ensera.
   ```

The [Java guide](java.md) adds Spring Boot wiring, compaction and saved sessions.

### Coding agents: MCP

A coding agent such as Claude Code needs no adapter: Taisce serves MCP at `/mcp` on the same port,
with the same token.

```bash
claude mcp add --transport http taisce http://localhost:8080/mcp \
  --header "Authorization: Bearer $TAISCE_TOKEN"
claude mcp list
```

You should see:

```text
taisce: http://localhost:8080/mcp (HTTP) - ✔ Connected
```

Claude Code keeps the header in its own configuration on your machine, not in your project. The
[MCP guide](mcp.md) lists the six tools, and its [Claude Code plugin](mcp.md#the-claude-code-plugin)
adds what memory knows at the start of a session.

**Didn't work?** If the second run doesn't know about Cork, check that `wait_formed` returned before
it and that both runs name the same `alice`: the two runs are separate conversations, and only Taisce
connects them. If your error callback prints `taisce recall failed` or
`taisce observe failed`, the program can't reach `TAISCE_API` or the token is wrong
([every call answers 401](troubleshooting.md#every-call-answers-401-unauthenticated)).

## When you are done

`docker compose down` stops everything. Your data stays in a Docker volume, so
`docker compose up -d` brings it back, and your token still works.

## Next steps

- [How it works](../start/how-it-works.md): what happened in each step, in plain words.
- [Examples](../examples/overview.md): recipes for preferences, citations, forgetting, long
  conversations and coding agents.
- [The HTTP API, by task](http-api.md): every call, option and error.
- [Adapters](adapters.md), then the guide for [Python](python.md), [.NET](dotnet.md) or
  [Java](java.md): options, compaction for long conversations, saved sessions and known limits.
- [Troubleshooting](troubleshooting.md): the problems a first run actually hits.
