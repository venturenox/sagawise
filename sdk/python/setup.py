from setuptools import setup, find_packages

# Load the README file as long_description
with open("README.md", "r") as fh:
    long_description = fh.read()

setup(
    name="sagawise",  # Name of the package
    version="0.1.0",  # Initial release version
    author="Venturenox",  # Your name (or company/team)
    author_email="queries@venturenox.com",  # Your email
    description="A Python package for interacting with Sagawise workflows",  # Short description
    long_description=long_description,  # Long description from README.md
    long_description_content_type="text/markdown",  # README format
    url="https://github.com/venturenox/sagawise",  # URL to the repository or project homepage
    packages=find_packages(),  # Automatically find packages in the source directory
    install_requires=[  # Required dependencies
        "requests>=2.0.0",  # Specify your dependency and version
    ],
    classifiers=[
    "Programming Language :: Python :: 3",  # Specify the Python versions
    "License :: OSI Approved :: Apache Software License",  # License type
    "Operating System :: OS Independent",
    ],
    python_requires='>=3.6',  # Minimum Python version required
)
