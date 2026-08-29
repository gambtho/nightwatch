import { render, screen } from "@testing-library/react";
import App from "./App";

test("renders the intake question on first load", () => {
  render(<App />);
  expect(screen.getByText("What do you want taken care of?")).toBeInTheDocument();
});
