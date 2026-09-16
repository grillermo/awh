## Claude Instruction: Python Environment Policy

To ensure project stability and keep my system clean, always adhere to the following workflow when suggesting or performing Python library installations:

### 1. Mandatory Virtual Environment Use
Before suggesting any `pip install` command, check for or initialize a virtual environment.
* **Preferred Directory:** `python_env`
* **Standard Activation:**
    * **macOS/Linux:** `source python_env/bin/activate`

### 2. Implementation Workflow
When I need a new library, provide the instructions in this specific order:

1.  **Check/Create:** If `.venv` doesn't exist, create it:
    ```bash
    python -m venv python_env
    ```
2.  **Activate:** Provide the activation command for my OS.
3.  **Install:** Execute the installation within the active environment.
4.  **Record:** Update `requirements.txt` after installation

### 3. Example Response Format
> "To install **requests**, please run the following:"
> 
> ```bash
> # 1. Create environment (if not already done)
> python3 -m venv python_env
> 
> # 2. Activate
> source python_env/bin/activate
> 
> # 3. Install
> pip3 install requests
> 
> # 4. Freeze dependencies
> pip freeze > requirements.txt
> ```

---

## Simulators

- **Only two simulators exist on this machine** — `iPhone 16e`
  (`08D1BCF0-862E-44C7-9A2B-3EF107A4FB69`) and `iPad mini (A17 Pro)`
  (`C0D83EFE-4F6C-4324-8743-6F77259BE12C`), both on iOS 26.3. Every other
  device was deleted to reclaim disk. Do not create new ones: pick one of these
  two, by udid. Older docs and `.superpowers/` reports name `iPhone 17 Pro`;
  that device is gone and those commands will fail as written.
