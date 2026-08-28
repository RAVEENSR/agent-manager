export interface Message {
  id: string;
  role: "user" | "agent" | "error";
  text: string;
}
