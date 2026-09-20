import { MarkdownBody } from "./MarkdownBody";
import { splitAssistantPresentation } from "../wrapper/assistantPresentation";

export function ChatAssistantBody({ content }: { content: string }) {
  const { reasoning, reply } = splitAssistantPresentation(content);
  return (
    <>
      {reasoning ? (
        <details className="chat-reasoning">
          <summary>Reasoning</summary>
          <div className="chat-reasoning-body">
            <MarkdownBody content={reasoning} />
          </div>
        </details>
      ) : null}
      <div className="chat-bubble-body">
        <MarkdownBody content={reply} />
      </div>
    </>
  );
}
