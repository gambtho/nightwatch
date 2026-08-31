import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import VerdictScreen from "./Verdict";
import { supportDigestVerdict } from "../fixtures/verdict";

test("shows all three blocks", () => {
  render(<VerdictScreen verdict={supportDigestVerdict} onReview={() => {}} />);
  expect(screen.getByText("I CAN DO THIS")).toBeInTheDocument();
  expect(screen.getByText("I'D GET THIS WRONG")).toBeInTheDocument();
  expect(screen.getByText("I'D NEED ACCESS TO")).toBeInTheDocument();
});

test("the fixture verdict names at least one limitation", () => {
  expect(supportDigestVerdict.cannot.length).toBeGreaterThan(0);
});

test("the review button reports the user's intent", async () => {
  const onReview = vi.fn();
  render(<VerdictScreen verdict={supportDigestVerdict} onReview={onReview} />);
  await userEvent.click(screen.getByRole("button", { name: "Review what it can touch" }));
  expect(onReview).toHaveBeenCalledOnce();
});
